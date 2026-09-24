package http

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const (
	oidcFlowLifetime           = 10 * time.Minute
	maxReturnPathCharacters    = 2048
	maxReturnLocationBytes     = 8192
	maxOIDCCallbackStateBytes  = 512
	maxOIDCCallbackCodeBytes   = 4096
	maxOIDCCallbackErrorBytes  = 256
	oidcMaxConcurrentExchanges = 4
	oidcRetryAfter             = 5 * time.Second
	oidcStateKey               = "oidc_state"
	oidcNonceKey               = "oidc_nonce"
	oidcVerifierKey            = "oidc_verifier"
	oidcReturnPathKey          = "oidc_return_path"
	oidcExpiryKey              = "oidc_expiry"
)

type oidcBrowserError string

const (
	oidcErrorUnknownIdentity      oidcBrowserError = "unknown_identity"
	oidcErrorProvisioningDisabled oidcBrowserError = "provisioning_disabled"
	oidcErrorStateInvalid         oidcBrowserError = "state_invalid"
	oidcErrorTokenInvalid         oidcBrowserError = "token_invalid"
	oidcErrorProviderUnavailable  oidcBrowserError = "provider_unavailable"
	oidcErrorDisabled             oidcBrowserError = "disabled"
	oidcErrorInternal             oidcBrowserError = "internal_error"
)

type authProviderResponse struct {
	Providers []authProvider `json:"providers"`
}

type authProvider struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type oidcFlow struct {
	state, nonce, verifier, returnPath string
	expiresAt                          time.Time
}

type oidcCallbackParams struct {
	state, code, providerError string
}

func (s *Server) handleAuthProviders(w http.ResponseWriter, r *http.Request) {
	providers := []authProvider{{ID: "local", DisplayName: "Local"}}
	if s.oidcProvider != nil && s.oidcConfig.Enabled {
		providers = append(providers, authProvider{ID: "oidc", DisplayName: s.oidcConfig.DisplayName})
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, r, s.logger, http.StatusOK, authProviderResponse{Providers: providers})
}

func (s *Server) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r, s.trustedProxyCIDRs)
	if allowed, retry := s.loginLimiter.allowOne("oidc-start-ip:" + ip); !allowed {
		s.writeOIDCStartRateLimited(w, r, retry)
		return
	}
	returnPath, fields, err := decodeOIDCStartForm(r)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return
	}
	flow, err := s.newOIDCFlow(returnPath)
	if err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		writeError(w, r, s.logger, err)
		return
	}
	s.putOIDCFlow(r.Context(), flow)
	challenge := sha256.Sum256([]byte(flow.verifier))
	authorizationURL := s.oidcProvider.AuthorizationURL(
		flow.state, flow.nonce, base64.RawURLEncoding.EncodeToString(challenge[:]),
	)
	writeRedirect(w, authorizationURL)
}

func decodeOIDCStartForm(r *http.Request) (string, []httputil.FieldError, error) {
	empty, err := requestBodyEmpty(r)
	if err != nil {
		return "", nil, fmt.Errorf("read OIDC start form: %w", core.ErrInvalidArgument)
	}
	if empty {
		return "", nil, nil
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
		return "", nil, errUnsupportedMediaType
	}
	if err := r.ParseForm(); err != nil {
		return "", nil, fmt.Errorf("parse OIDC start form: %w", core.ErrInvalidArgument)
	}
	for name := range r.PostForm {
		if name != "return_to" {
			return "", []httputil.FieldError{{Field: name, Code: "unknown", Message: "field is not recognized"}}, nil
		}
	}
	values := r.PostForm["return_to"]
	if len(values) > 1 {
		return "", []httputil.FieldError{{Field: "return_to", Code: "duplicate", Message: "field must appear at most once"}}, nil
	}
	if len(values) == 0 {
		return "", nil, nil
	}
	if utf8.RuneCountInString(values[0]) > maxReturnPathCharacters {
		return "", []httputil.FieldError{{Field: "return_to", Code: "too_long", Message: "return path is too long"}}, nil
	}
	return values[0], nil, nil
}

func requestBodyEmpty(r *http.Request) (bool, error) {
	prefix, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil {
		return false, err
	}
	if len(prefix) == 0 {
		return true, nil
	}
	r.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(strings.NewReader(string(prefix)), r.Body), Closer: r.Body}
	return false, nil
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	r, cancel := s.withAuthenticationDeadline(r)
	defer cancel()
	flow, params, ok := s.beginOIDCCallback(w, r)
	if !ok {
		return
	}
	claims, ok := s.exchangeOIDCClaims(w, r, flow, params.code)
	if !ok {
		return
	}
	result, err := s.resolveOIDCAccount(r.Context(), claims)
	if err != nil {
		s.writeOIDCAccountFailure(w, r, err)
		return
	}
	s.emitOIDCRoleChanges(r, result)
	if err := s.sessions.RenewToken(r.Context()); err != nil {
		s.writeOIDCInternalError(w, r, err)
		return
	}
	s.sessions.Put(r.Context(), sessionAccountIDKey, result.Account.ID)
	s.completeOIDCLogin(r, result.Account)
	writeRedirect(w, flow.returnPath)
}

func (s *Server) emitOIDCRoleChanges(r *http.Request, result core.OIDCSignInResult) {
	for _, role := range result.AddedRoles {
		s.emitOIDCRoleAudit(r, result.Account.ID, role, telemetry.AuditActionRoleAssign, "oidc_granted")
	}
	for _, role := range result.RemovedRoles {
		s.emitOIDCRoleAudit(r, result.Account.ID, role, telemetry.AuditActionRoleRemove, "oidc_removed")
	}
}

func (s *Server) emitOIDCRoleAudit(r *http.Request, accountID, role, action, reason string) {
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: accountID, Action: action, Resource: accountResource(accountID), Role: role,
		Result: telemetry.AuditSuccess, Reason: reason, Source: clientIP(r, s.trustedProxyCIDRs),
		Provider: telemetry.AuditProviderOIDC, RequestID: requestIDFrom(r.Context()),
	})
	if err == nil {
		return
	}
	s.auditFailureMetrics.IncAuditWriteFailure()
	s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
}

func (s *Server) beginOIDCCallback(w http.ResponseWriter, r *http.Request) (oidcFlow, oidcCallbackParams, bool) {
	ip := clientIP(r, s.trustedProxyCIDRs)
	if allowed, retry := s.loginLimiter.allowOne("oidc-ip:" + ip); !allowed {
		s.writeOIDCRateLimited(w, r, retry)
		return oidcFlow{}, oidcCallbackParams{}, false
	}
	params, invalid := decodeOIDCCallbackParams(r.URL.RawQuery)
	if invalid != "" {
		s.writeOIDCParameterFailure(w, r, invalid)
		return oidcFlow{}, oidcCallbackParams{}, false
	}
	flow, err := s.claimOIDCFlow(r.Context(), params.state)
	if err != nil {
		if errors.Is(err, core.ErrOIDCRejected) {
			s.writeOIDCCallbackFailure(w, r, oidcErrorStateInvalid, "state_invalid", err)
		} else {
			s.writeOIDCInternalError(w, r, err)
		}
		return oidcFlow{}, oidcCallbackParams{}, false
	}
	if params.providerError != "" || params.code == "" {
		s.writeOIDCCallbackFailure(w, r, oidcErrorTokenInvalid, "token_invalid", core.ErrOIDCRejected)
		return oidcFlow{}, oidcCallbackParams{}, false
	}
	return flow, params, true
}

func (s *Server) claimOIDCFlow(ctx context.Context, state string) (oidcFlow, error) {
	flow, err := s.readOIDCFlow(ctx)
	if err != nil {
		return oidcFlow{}, err
	}
	if !sameSecret(flow.state, state) {
		return oidcFlow{}, core.ErrOIDCRejected
	}
	claimed, err := s.oidcFlows.ClaimOIDCFlow(ctx, s.sessions.Token(ctx))
	if err != nil {
		return oidcFlow{}, err
	}
	if !claimed {
		return oidcFlow{}, core.ErrOIDCRejected
	}
	if err := s.sessions.Destroy(ctx); err != nil {
		return oidcFlow{}, err
	}
	return flow, nil
}

func decodeOIDCCallbackParams(rawQuery string) (oidcCallbackParams, string) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return oidcCallbackParams{}, "state"
	}
	state, valid := singleBoundedQueryValue(query, "state", maxOIDCCallbackStateBytes)
	if !valid {
		return oidcCallbackParams{}, "state"
	}
	code, valid := singleBoundedQueryValue(query, "code", maxOIDCCallbackCodeBytes)
	if !valid {
		return oidcCallbackParams{}, "code"
	}
	providerError, valid := singleBoundedQueryValue(query, "error", maxOIDCCallbackErrorBytes)
	if !valid {
		return oidcCallbackParams{}, "error"
	}
	return oidcCallbackParams{state: state, code: code, providerError: providerError}, ""
}

func singleBoundedQueryValue(query url.Values, name string, maximum int) (string, bool) {
	values := query[name]
	if len(values) > 1 {
		return "", false
	}
	if len(values) == 0 {
		return "", true
	}
	if !asciiString(values[0]) || len(values[0]) > maximum {
		return "", false
	}
	return values[0], true
}

func asciiString(value string) bool {
	for _, char := range []byte(value) {
		if char > 0x7f {
			return false
		}
	}
	return true
}

func (s *Server) writeOIDCParameterFailure(w http.ResponseWriter, r *http.Request, name string) {
	if name == "state" {
		s.writeOIDCCallbackFailure(w, r, oidcErrorStateInvalid, "state_invalid", core.ErrOIDCRejected)
		return
	}
	s.writeOIDCCallbackFailure(w, r, oidcErrorTokenInvalid, "token_invalid", core.ErrOIDCRejected)
}

func (s *Server) exchangeOIDCClaims(
	w http.ResponseWriter, r *http.Request, flow oidcFlow, code string,
) (core.OIDCClaims, bool) {
	if !s.acquireOIDCExchange() {
		w.Header().Set("Retry-After", "1")
		s.writeOIDCCallbackFailure(w, r, oidcErrorProviderUnavailable, "overloaded", errAuthenticationBusy)
		return core.OIDCClaims{}, false
	}
	defer s.releaseOIDCExchange()
	claims, err := s.oidcProvider.Exchange(r.Context(), code, flow.verifier, flow.nonce)
	if err != nil {
		if errors.Is(err, core.ErrOIDCProviderUnavailable) {
			w.Header().Set("Retry-After", strconv.Itoa(int(oidcRetryAfter/time.Second)))
			s.writeOIDCCallbackFailure(w, r, oidcErrorProviderUnavailable, "provider_unavailable", err)
			return core.OIDCClaims{}, false
		}
		if errors.Is(err, core.ErrOIDCRejected) {
			s.writeOIDCCallbackFailure(w, r, oidcErrorTokenInvalid, "token_invalid", err)
			return core.OIDCClaims{}, false
		}
		s.writeOIDCInternalError(w, r, fmt.Errorf("OpenID Connect token exchange failed: %w", err))
		return core.OIDCClaims{}, false
	}
	return claims, true
}

func (s *Server) acquireOIDCExchange() bool {
	select {
	case s.oidcExchanges <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseOIDCExchange() { <-s.oidcExchanges }

func (s *Server) newOIDCFlow(returnPath string) (oidcFlow, error) {
	state, err := randomFlowValue()
	if err != nil {
		return oidcFlow{}, err
	}
	nonce, err := randomFlowValue()
	if err != nil {
		return oidcFlow{}, err
	}
	verifier, err := randomFlowValue()
	if err != nil {
		return oidcFlow{}, err
	}
	return oidcFlow{
		state: state, nonce: nonce, verifier: verifier,
		returnPath: safeReturnPath(returnPath, s.publicURL),
		expiresAt:  s.clockNow().Add(oidcFlowLifetime),
	}, nil
}

func randomFlowValue() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate OIDC flow secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func (s *Server) putOIDCFlow(ctx context.Context, flow oidcFlow) {
	s.sessions.Put(ctx, oidcStateKey, flow.state)
	s.sessions.Put(ctx, oidcNonceKey, flow.nonce)
	s.sessions.Put(ctx, oidcVerifierKey, flow.verifier)
	s.sessions.Put(ctx, oidcReturnPathKey, flow.returnPath)
	s.sessions.Put(ctx, oidcExpiryKey, flow.expiresAt.UnixMicro())
}

func (s *Server) readOIDCFlow(ctx context.Context) (oidcFlow, error) {
	flow := oidcFlow{
		state:      s.sessions.GetString(ctx, oidcStateKey),
		nonce:      s.sessions.GetString(ctx, oidcNonceKey),
		verifier:   s.sessions.GetString(ctx, oidcVerifierKey),
		returnPath: s.sessions.GetString(ctx, oidcReturnPathKey),
		expiresAt:  time.UnixMicro(s.sessions.GetInt64(ctx, oidcExpiryKey)),
	}
	if flow.state == "" || flow.nonce == "" || flow.verifier == "" || !s.clockNow().Before(flow.expiresAt) {
		return oidcFlow{}, core.ErrOIDCRejected
	}
	return flow, nil
}

func (s *Server) resolveOIDCAccount(ctx context.Context, claims core.OIDCClaims) (core.OIDCSignInResult, error) {
	roles := core.MapOIDCRoles(claims.RoleValues, s.oidcConfig.RoleMap)
	return s.oidcAccounts.SignInOIDC(ctx, core.OIDCSignIn{
		Provider: "oidc", Issuer: claims.Issuer, Subject: claims.Subject, UsernameClaim: claims.Username,
		MappedRoles: roles, DefaultRole: s.oidcConfig.DefaultRole,
		AutoProvision: s.oidcConfig.DefaultRole != "",
		Now:           s.clockNow(),
	})
}

func (s *Server) completeOIDCLogin(r *http.Request, account core.Account) {
	state := sessionState(r.Context())
	state.afterCommit = func() {
		s.metrics.IncLoginAttempt("oidc", "success")
		s.emitOIDCAudit(r, account.ID, accountResource(account.ID), telemetry.AuditSuccess, "authenticated")
	}
	state.onCommitFailed = func(error) {
		s.metrics.IncLoginAttempt("oidc", "internal_error")
		s.emitOIDCAudit(r, "anonymous", routeResource(r), telemetry.AuditFailure, "internal_error")
	}
}

func (s *Server) writeOIDCAccountFailure(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, core.ErrNotFound) {
		err = errors.New("configured OpenID Connect role is unavailable")
	}
	reason := "internal_error"
	browserCode := oidcErrorInternal
	switch {
	case errors.Is(err, core.ErrOIDCProvisioningDisabled):
		reason = "provisioning_disabled"
		browserCode = oidcErrorProvisioningDisabled
	case errors.Is(err, core.ErrForbidden):
		reason = "unknown_identity"
		browserCode = oidcErrorUnknownIdentity
	default:
		if failure, ok := errors.AsType[*core.CredentialFailure](err); ok {
			reason = string(failure.Reason)
			if failure.Reason == core.CredentialDisabled {
				browserCode = oidcErrorDisabled
			}
		}
	}
	s.writeOIDCCallbackFailure(w, r, browserCode, reason, err)
}

func (s *Server) writeOIDCCallbackFailure(
	w http.ResponseWriter, r *http.Request, browserCode oidcBrowserError, reason string, err error,
) {
	s.metrics.IncLoginAttempt("oidc", reason)
	s.emitOIDCAudit(r, "anonymous", routeResource(r), telemetry.AuditFailure, reason)
	if acceptsHTML(r.Header.Get("Accept")) {
		logOIDCRequestError(r, s.logger, err)
		s.writeOIDCBrowserRedirect(w, browserCode)
		return
	}
	writeOIDCError(w, r, s.logger, err)
}

func (s *Server) writeOIDCInternalError(w http.ResponseWriter, r *http.Request, err error) {
	s.metrics.IncLoginAttempt("oidc", "internal_error")
	s.emitOIDCAudit(r, "anonymous", routeResource(r), telemetry.AuditFailure, "internal_error")
	if acceptsHTML(r.Header.Get("Accept")) {
		logOIDCRequestError(r, s.logger, err)
		s.writeOIDCBrowserRedirect(w, oidcErrorInternal)
		return
	}
	writeOIDCError(w, r, s.logger, err)
}

func (s *Server) writeOIDCRateLimited(w http.ResponseWriter, r *http.Request, retry time.Duration) {
	seconds := max(1, int((retry+time.Second-1)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	s.writeOIDCCallbackFailure(w, r, oidcErrorProviderUnavailable, "rate_limited", errRateLimited)
}

func (s *Server) writeOIDCStartRateLimited(w http.ResponseWriter, r *http.Request, retry time.Duration) {
	seconds := max(1, int((retry+time.Second-1)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	s.metrics.IncLoginAttempt("oidc", "rate_limited")
	s.emitOIDCAudit(r, "anonymous", routeResource(r), telemetry.AuditFailure, "rate_limited")
	writeError(w, r, s.logger, errRateLimited)
}

func (s *Server) writeOIDCBrowserRedirect(w http.ResponseWriter, code oidcBrowserError) {
	location := strings.TrimRight(s.publicURL, "/") + "/login?error=" + url.QueryEscape(string(code))
	writeRedirect(w, location)
}

func (s *Server) emitOIDCAudit(
	r *http.Request, actor, resource string, result telemetry.AuditResult, reason string,
) {
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: actor, Action: "auth.login", Resource: resource, Result: result,
		Reason: reason, Source: clientIP(r, s.trustedProxyCIDRs), Provider: telemetry.AuditProviderOIDC,
		RequestID: requestIDFrom(r.Context()),
	})
	if err != nil {
		s.auditFailureMetrics.IncAuditWriteFailure()
		s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", "auth.login")
	}
}

func acceptsHTML(value string) bool {
	for mediaRange := range strings.SplitSeq(value, ",") {
		mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(mediaRange))
		if err == nil && strings.EqualFold(mediaType, "text/html") {
			return true
		}
	}
	return false
}

func sameSecret(expected, actual string) bool {
	return len(expected) == len(actual) && subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) == 1
}

func safeReturnPath(raw, publicURL string) string {
	if raw == "" || !utf8.ValidString(raw) || utf8.RuneCountInString(raw) > maxReturnPathCharacters ||
		!strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	for _, char := range raw {
		if char == '\\' || char < 0x20 || char == 0x7f {
			return "/"
		}
	}
	base, baseErr := url.Parse(publicURL)
	destination, err := url.Parse(raw)
	if baseErr != nil || err != nil || destination.IsAbs() || destination.Host != "" {
		return "/"
	}
	resolved := base.ResolveReference(destination)
	if resolved.Scheme != base.Scheme || !strings.EqualFold(resolved.Host, base.Host) || isLoginPath(resolved.Path) {
		return "/"
	}
	location := resolved.RequestURI() + fragmentSuffix(resolved.Fragment)
	if len(location) > maxReturnLocationBytes {
		return "/"
	}
	return location
}

func isLoginPath(path string) bool {
	return strings.EqualFold(strings.TrimRight(path, "/"), "/login")
}

func fragmentSuffix(fragment string) string {
	if fragment == "" {
		return ""
	}
	parsed := &url.URL{Fragment: fragment}
	return "#" + parsed.EscapedFragment()
}

func writeRedirect(w http.ResponseWriter, location string) {
	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) clockNow() time.Time {
	return s.clock.Now()
}
