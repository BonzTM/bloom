package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type accountResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeLogin(w, r)
	if !ok {
		return
	}
	ip := clientIP(r, s.trustedProxyCIDRs)
	if !s.acquirePasswordVerification() {
		s.writeAuthenticationBusy(w, r, request.Username, ip)
		return
	}
	allowed, retry := s.loginLimiter.allow("ip:"+ip, "username:"+request.Username)
	if !allowed {
		s.releasePasswordVerification()
		s.writeRateLimited(w, r, request.Username, ip, retry)
		return
	}
	var cancel context.CancelFunc
	r, cancel = s.withAuthenticationDeadline(r)
	defer cancel()
	account, err := s.verifyCredentials(r.Context(), request.Username, request.Password)
	if err != nil {
		s.writeLoginFailure(w, r, request.Username, ip, err)
		return
	}
	s.completeLogin(w, r, account, request.Username, ip)
}

func (s *Server) completeLogin(
	w http.ResponseWriter,
	r *http.Request,
	account core.Account,
	username, ip string,
) {
	if err := s.revokeInboundSession(r.Context()); err != nil {
		s.writeLoginInternalError(w, r, username, ip, err)
		return
	}
	snapshot, err := s.loadAuthorizationSnapshot(r.Context(), account.ID)
	if err != nil {
		s.writeLoginInternalError(w, r, username, ip, err)
		return
	}
	if err := s.createAuthenticatedSession(r.Context(), account.ID); err != nil {
		s.writeLoginInternalError(w, r, username, ip, err)
		return
	}
	s.setLoginCommitTelemetry(sessionState(r.Context()), r, account.ID, ip)
	writeJSON(w, r, s.logger, http.StatusOK, currentAccountDTO(account, snapshot))
}

func (s *Server) revokeInboundSession(ctx context.Context) error {
	state := sessionState(ctx)
	if state == nil || !state.inbound {
		return nil
	}
	state.clearCookie = true
	if !state.resolved {
		return nil
	}
	if err := s.sessions.Destroy(ctx); err != nil {
		return fmt.Errorf("destroy inbound session: %w", err)
	}
	return nil
}

func (s *Server) createAuthenticatedSession(ctx context.Context, accountID string) error {
	if err := s.sessions.RenewToken(ctx); err != nil {
		return err
	}
	s.sessions.Put(ctx, sessionAccountIDKey, accountID)
	sessionState(ctx).replacementReady = true
	return nil
}

func (s *Server) setLoginCommitTelemetry(state *sessionRequestState, r *http.Request, accountID, ip string) {
	state.afterCommit = func() {
		s.metrics.IncLoginAttempt("local", "success")
		s.emitLocalLoginAudit(r, accountID, "", accountResource(accountID), telemetry.AuditSuccess, "authenticated", ip)
	}
	state.onCommitFailed = func(error) {
		s.metrics.IncLoginAttempt("local", "internal_error")
		s.emitLocalLoginAudit(r, accountID, "", accountResource(accountID), telemetry.AuditFailure, "internal_error", ip)
	}
}

func (s *Server) acquirePasswordVerification() bool {
	select {
	case s.passwordVerifications <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releasePasswordVerification() { <-s.passwordVerifications }

func (s *Server) verifyCredentials(ctx context.Context, username, password string) (core.Account, error) {
	account, err := s.authenticateWithSlot(ctx, username, password)
	if err != nil {
		return core.Account{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.Account{}, fmt.Errorf("authentication deadline: %w", err)
	}
	return account, nil
}

func (s *Server) authenticateWithSlot(ctx context.Context, username, password string) (core.Account, error) {
	defer s.releasePasswordVerification()
	return s.identity.Authenticate(ctx, username, password)
}

func (s *Server) withAuthenticationDeadline(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), s.authOperationTimeout)
	state := sessionState(ctx)
	state.authDeadline, _ = ctx.Deadline()
	return r.WithContext(ctx), cancel
}

func (s *Server) decodeLogin(w http.ResponseWriter, r *http.Request) (loginRequest, bool) {
	if !loginContentTypeSupported(r.Header.Get("Content-Type")) {
		writeError(w, r, s.logger, errUnsupportedMediaType)
		return loginRequest{}, false
	}
	request, err := httputil.DecodeJSON[loginRequest](w, r, s.maxBodyBytes)
	if err != nil {
		s.writeValidation(w, r, []httputil.FieldError{{Field: "body", Code: "invalid", Message: "must be one valid JSON object"}})
		return loginRequest{}, false
	}
	fields := make([]httputil.FieldError, 0, 2)
	if request.Username == "" {
		fields = append(fields, httputil.FieldError{Field: "username", Code: "required", Message: "username is required"})
	} else if utf8.RuneCountInString(request.Username) > core.MaxSubmittedUsernameCharacters {
		fields = append(fields, httputil.FieldError{Field: "username", Code: "too_long", Message: "username is too long"})
	} else if key, usernameErr := core.UsernameKey(request.Username); usernameErr != nil {
		fields = append(fields, httputil.FieldError{Field: "username", Code: "invalid", Message: "username is invalid"})
	} else {
		request.Username = key
	}
	if request.Password == "" {
		fields = append(fields, httputil.FieldError{Field: "password", Code: "required", Message: "password is required"})
	} else if len(request.Password) > core.MaxPasswordBytes || utf8.RuneCountInString(request.Password) > core.MaxPasswordCharacters {
		fields = append(fields, httputil.FieldError{Field: "password", Code: "too_long", Message: "password is too long"})
	}
	if len(fields) > 0 {
		s.writeValidation(w, r, fields)
		return loginRequest{}, false
	}
	return request, true
}

var errUnsupportedMediaType = errors.New("content type must be application/json")

func loginContentTypeSupported(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func (s *Server) writeValidation(w http.ResponseWriter, r *http.Request, fields []httputil.FieldError) {
	writeJSON(w, r, s.logger, http.StatusUnprocessableEntity, httputil.ErrorResponse{
		Code: codeValidationFailed, Message: "request validation failed",
		Fields: fields, RequestID: requestIDFrom(r.Context()),
	})
}

func (s *Server) writeLoginFailure(w http.ResponseWriter, r *http.Request, username, ip string, err error) {
	var failure *core.CredentialFailure
	if !errors.As(err, &failure) {
		s.writeLoginInternalError(w, r, username, ip, err)
		return
	}
	reason := string(failure.Reason)
	s.metrics.IncLoginAttempt("local", reason)
	s.emitFailedLoginAudit(r, username, reason, ip)
	writeError(w, r, s.logger, core.ErrInvalidCredentials)
}

func (s *Server) writeLoginInternalError(w http.ResponseWriter, r *http.Request, username, ip string, err error) {
	s.metrics.IncLoginAttempt("local", "internal_error")
	s.emitFailedLoginAudit(r, username, "internal_error", ip)
	writeError(w, r, s.logger, err)
}

func (s *Server) writeRateLimited(w http.ResponseWriter, r *http.Request, username, ip string, retry time.Duration) {
	seconds := max(1, int((retry+time.Second-1)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	s.metrics.IncLoginAttempt("local", "rate_limited")
	s.emitFailedLoginAudit(r, username, "rate_limited", ip)
	writeError(w, r, s.logger, errRateLimited)
}

func (s *Server) writeAuthenticationBusy(w http.ResponseWriter, r *http.Request, username, ip string) {
	w.Header().Set("Retry-After", "1")
	s.metrics.IncLoginAttempt("local", "overloaded")
	s.emitFailedLoginAudit(r, username, "overloaded", ip)
	writeError(w, r, s.logger, errAuthenticationBusy)
}

func (s *Server) emitAuthAudit(
	r *http.Request,
	actor, action, resource string,
	result telemetry.AuditResult,
	reason, ip string,
) {
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: actor, Action: action, Resource: resource, Result: result,
		Reason: reason, Source: ip, RequestID: requestIDFrom(r.Context()),
	})
	if err == nil {
		return
	}
	s.auditFailureMetrics.IncAuditWriteFailure()
	s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", action)
}

func (s *Server) emitFailedLoginAudit(r *http.Request, username, reason, ip string) {
	s.emitLocalLoginAudit(
		r, "anonymous", s.usernameSubjectID(username), routeResource(r), telemetry.AuditFailure, reason, ip,
	)
}

func (s *Server) emitLocalLoginAudit(
	r *http.Request,
	actor, subjectID, resource string,
	result telemetry.AuditResult,
	reason, ip string,
) {
	err := s.audit.Emit(r.Context(), telemetry.AuditEvent{
		Actor: actor, SubjectID: subjectID, Action: "auth.login", Resource: resource, Result: result,
		Reason: reason, Source: ip, Provider: telemetry.AuditProviderLocal, RequestID: requestIDFrom(r.Context()),
	})
	if err == nil {
		return
	}
	s.auditFailureMetrics.IncAuditWriteFailure()
	s.logger.ErrorContext(r.Context(), "write audit event", "error", err, "action", "auth.login")
}

const (
	auditResourceRouteUnmatched   = "route:unmatched"
	auditResourceAuthLogin        = "route:auth.login"
	auditResourceAuthLogout       = "route:auth.logout"
	auditResourceAuthMe           = "route:auth.me"
	auditResourceAuthPermissions  = "route:auth.permissions"
	auditResourceAuthProviders    = "route:auth.providers"
	auditResourceAuthOIDCStart    = "route:auth.oidc.start"
	auditResourceAuthOIDCCallback = "route:auth.oidc.callback"
	auditResourceRoles            = "route:roles"
	auditResourceMediaServers     = "route:media_servers"
	auditResourceDownloadManagers = "route:download_managers"
	auditResourceInvites          = "route:invites"
	auditResourceInvitePublic     = "route:invite_public"
	auditResourcePlayback         = "route:playback"
	auditResourceMetadata         = "route:metadata"
	auditResourceMetadataSettings = "route:metadata.settings"
	auditResourceRequestProfiles  = "route:request_profiles"
	auditResourceRequests         = "route:requests"
	auditResourceRequestQuotas    = "route:request_quotas"
)

func routeResource(r *http.Request) string {
	switch r.Pattern {
	case "/api/v1/auth/login":
		return auditResourceAuthLogin
	case "/api/v1/auth/logout":
		return auditResourceAuthLogout
	case "/api/v1/auth/me":
		return auditResourceAuthMe
	case "/api/v1/auth/permissions":
		return auditResourceAuthPermissions
	case "/api/v1/auth/providers":
		return auditResourceAuthProviders
	case "/api/v1/auth/oidc/start":
		return auditResourceAuthOIDCStart
	case "/api/v1/auth/oidc/callback":
		return auditResourceAuthOIDCCallback
	case "/api/v1/roles":
		return auditResourceRoles
	case "/api/v1/media-servers", "/api/v1/media-servers/{id}",
		"/api/v1/media-servers/{id}/probe", "/api/v1/media-servers/{id}/libraries":
		return auditResourceMediaServers
	case "/api/v1/download-managers", "/api/v1/download-managers/{id}", "/api/v1/download-managers/{id}/options":
		return auditResourceDownloadManagers
	case "/api/v1/invites", "/api/v1/invites/{id}":
		return auditResourceInvites
	case "/api/v1/invite/{code}", "/api/v1/invite/{code}/accept":
		return auditResourceInvitePublic
	case "/api/v1/playback/now", "/api/v1/playback/history":
		return auditResourcePlayback
	case "/api/v1/metadata/search", "/api/v1/metadata/movies/{id}", "/api/v1/metadata/series/{id}":
		return auditResourceMetadata
	case "/api/v1/metadata/providers/tmdb/key":
		return auditResourceMetadataSettings
	case "/api/v1/request-profiles", "/api/v1/request-profiles/{id}":
		return auditResourceRequestProfiles
	case "/api/v1/requests", "/api/v1/requests/{id}", "/api/v1/requests/{id}/progress",
		"/api/v1/requests/{id}/approve", "/api/v1/requests/{id}/decline":
		return auditResourceRequests
	case "/api/v1/roles/{id}/request-quota", "/api/v1/accounts/{id}/request-quota":
		return auditResourceRequestQuotas
	default:
		return auditResourceRouteUnmatched
	}
}

const usernameAuditKeyContext = "bloom/audit/username-correlation/v1"

func deriveUsernameAuditKey(master []byte) [sha256.Size]byte {
	mac := hmac.New(sha256.New, master)
	if _, err := mac.Write([]byte(usernameAuditKeyContext)); err != nil {
		panic("sha256 HMAC rejected a write")
	}
	var key [sha256.Size]byte
	copy(key[:], mac.Sum(nil))
	return key
}

const usernameAuditIDBytes = 16

func (s *Server) usernameSubjectID(username string) string {
	key, err := core.UsernameKey(username)
	if err != nil {
		key = "invalid"
	}
	mac := hmac.New(sha256.New, s.usernameAuditKey[:])
	if _, err := mac.Write([]byte(key)); err != nil {
		panic("sha256 HMAC rejected a write")
	}
	return "username:" + hex.EncodeToString(mac.Sum(nil)[:usernameAuditIDBytes])
}

func accountResource(accountID string) string { return "account:" + accountID }

func accountDTO(account core.Account) accountResponse {
	return accountResponse{ID: account.ID, Username: account.Username}
}

func clientIP(r *http.Request, trusted []netip.Prefix) string {
	peer, err := parseRemoteAddress(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	if !addressTrusted(peer, trusted) {
		return peer.String()
	}
	forwarded, ok := forwardedHops(r.Header)
	if !ok {
		return peer.String()
	}
	for _, value := range slices.Backward(forwarded) {
		candidate, err := netip.ParseAddr(value)
		if err != nil {
			return peer.String()
		}
		candidate = candidate.Unmap()
		if !addressTrusted(candidate, trusted) {
			return candidate.String()
		}
	}
	return peer.String()
}

const maxForwardedHops = 64

func forwardedHops(header http.Header) ([]string, bool) {
	hops := make([]string, 0, 4)
	for _, field := range header.Values("X-Forwarded-For") {
		for value := range strings.SplitSeq(field, ",") {
			if len(hops) == maxForwardedHops {
				return nil, false
			}
			hops = append(hops, strings.TrimSpace(value))
		}
	}
	return hops, true
}

func parseRemoteAddress(remote string) (netip.Addr, error) {
	if addrPort, err := netip.ParseAddrPort(remote); err == nil {
		return addrPort.Addr().Unmap(), nil
	}
	addr, err := netip.ParseAddr(remote)
	if err != nil {
		return netip.Addr{}, err
	}
	return addr.Unmap(), nil
}

func addressTrusted(addr netip.Addr, trusted []netip.Prefix) bool {
	for _, prefix := range trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
