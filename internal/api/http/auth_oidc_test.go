package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2/memstore"
	"golang.org/x/oauth2"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
	"github.com/BonzTM/bloom/internal/testutil"
)

type fakeOIDCProvider struct {
	mu                    sync.Mutex
	claims                core.OIDCClaims
	err                   error
	beforeExchange        func(context.Context) error
	exchangeCalls         int
	code, verifier, nonce string
	challenge             string
	exchangeStarted       chan struct{}
	exchangeRelease       chan struct{}
}

func (p *fakeOIDCProvider) AuthorizationURL(state, nonce, challenge string) string {
	p.mu.Lock()
	p.challenge = challenge
	p.mu.Unlock()
	values := url.Values{"state": {state}, "nonce": {nonce}, "code_challenge": {challenge}}
	return "https://id.example/authorize?" + values.Encode()
}

func (p *fakeOIDCProvider) Exchange(ctx context.Context, code, verifier, nonce string) (core.OIDCClaims, error) {
	p.mu.Lock()
	p.exchangeCalls++
	p.code, p.verifier, p.nonce = code, verifier, nonce
	started, release := p.exchangeStarted, p.exchangeRelease
	beforeExchange := p.beforeExchange
	claims, err := p.claims, p.err
	p.mu.Unlock()
	if beforeExchange != nil {
		if hookErr := beforeExchange(ctx); hookErr != nil {
			return core.OIDCClaims{}, hookErr
		}
	}
	if started != nil {
		started <- struct{}{}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return core.OIDCClaims{}, ctx.Err()
		}
	}
	return claims, err
}

func (p *fakeOIDCProvider) exchanges() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exchangeCalls
}

type fakeOIDCAccountStore struct {
	mu           sync.Mutex
	result       core.OIDCSignInResult
	err          error
	input        core.OIDCSignIn
	beforeSignIn func(context.Context) error
}

func (s *fakeOIDCAccountStore) SignInOIDC(ctx context.Context, input core.OIDCSignIn) (core.OIDCSignInResult, error) {
	s.mu.Lock()
	s.input = input
	beforeSignIn := s.beforeSignIn
	result, err := s.result, s.err
	s.mu.Unlock()
	if beforeSignIn != nil {
		if hookErr := beforeSignIn(ctx); hookErr != nil {
			return core.OIDCSignInResult{}, hookErr
		}
	}
	return result, err
}

func newOIDCHarness(t *testing.T, mutate func(*config.AuthConfig, *config.OIDCConfig)) (authHarness, *fakeOIDCProvider, *fakeOIDCAccountStore) {
	t.Helper()
	hash, err := core.HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	accounts := &authAccountStore{accounts: map[string]core.Account{
		"alice": {ID: "11111111-1111-4111-8111-111111111111", Username: "alice", PasswordHash: &hash},
	}}
	authCfg := config.AuthConfig{
		SessionCookieSecure: true, SessionLifetime: time.Hour, SessionIdleTimeout: 15 * time.Minute,
		LoginRateRefillInterval: time.Minute, LoginRateBurst: 20, LoginRateMaxKeys: 100,
		LoginMaxConcurrent: 4,
	}
	oidcCfg := config.OIDCConfig{
		Enabled: true, DisplayName: "Company SSO", DefaultRole: "member",
		RoleClaim: "groups", RoleMap: map[string]string{"admins": "owner", "users": "member"},
	}
	if mutate != nil {
		mutate(&authCfg, &oidcCfg)
	}
	sessionStore := &controllableSessionStore{base: memstore.NewWithCleanupInterval(0)}
	sessions := newSessionManager(authCfg, sessionStore)
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	provider := &fakeOIDCProvider{claims: core.OIDCClaims{
		Issuer: "https://id.example", Subject: "subject-1", Username: "alice", RoleValues: []string{"users", "admins"},
	}}
	oidcAccounts := &fakeOIDCAccountStore{result: core.OIDCSignInResult{Account: accounts.accounts["alice"]}}
	audit := &recordingAudit{}
	metrics := &countingMetrics{}
	logs := &strings.Builder{}
	authorization := newAuthAuthorization()
	srv := New(config.HTTPConfig{
		Addr: ":0", ReadHeaderTimeout: time.Second, WriteTimeout: time.Second, MaxBodyBytes: 1 << 20,
	}, Deps{
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Metrics: metrics,
		Readiness: telemetry.NewReadiness(true), Pinger: &fakePinger{},
		Identity: core.NewLocalIdentityProvider(accounts), Accounts: accounts,
		Authorizer: authorization, Roles: authorization, Sessions: sessions,
		Audit: audit, AuditCorrelationKey: []byte("0123456789abcdef0123456789abcdef"),
		Clock: clock, Auth: authCfg, OIDC: provider, OIDCAccounts: oidcAccounts,
		OIDCFlows: sessionStore, OIDCConfig: oidcCfg, PublicURL: "https://bloom.example",
	})
	return authHarness{
		server: srv, h: srv.Handler(), store: accounts, sessions: sessions, audit: audit,
		metrics: metrics, clock: clock, logs: logs, authorization: authorization, sessionStore: sessionStore,
	}, provider, oidcAccounts
}

func startOIDC(t *testing.T, h authHarness, returnTo string) (*http.Cookie, string) {
	t.Helper()
	form := url.Values{"return_to": {returnTo}}.Encode()
	recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", form, nil, "application/x-www-form-urlencoded")
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d body %s", recorder.Code, recorder.Body.String())
	}
	location, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse start location: %v", err)
	}
	return sessionCookie(t, recorder), location.Query().Get("state")
}

func callbackOIDC(t *testing.T, h authHarness, cookie *http.Cookie, state string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&code=code-1"
	return h.request(t, http.MethodGet, path, "", cookie)
}

func TestOIDCCallbackUsesOneDeadlineForEveryPhase(t *testing.T) {
	h, provider, accounts := newOIDCHarness(t, nil)
	deadlines := make(chan time.Time, 4)
	capture := func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("callback phase has no deadline")
		}
		deadlines <- deadline
		return nil
	}
	h.sessionStore.beforeClaim = capture
	provider.beforeExchange = capture
	accounts.beforeSignIn = capture
	cookie, state := startOIDC(t, h, "/")
	h.sessionStore.beforeCommit = capture
	response := callbackOIDC(t, h, cookie, state)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d body %s", response.Code, response.Body.String())
	}
	if len(deadlines) != cap(deadlines) {
		t.Fatalf("bounded callback phases = %d, want %d", len(deadlines), cap(deadlines))
	}
	want := <-deadlines
	for range cap(deadlines) - 1 {
		if got := <-deadlines; !got.Equal(want) {
			t.Fatalf("callback phase deadline = %s, want shared deadline %s", got, want)
		}
	}
}

func TestOIDCCallbackDeadlineCancellationByPhase(t *testing.T) {
	tests := []struct {
		name  string
		block func(*authHarness, *fakeOIDCProvider, *fakeOIDCAccountStore)
	}{
		{name: "flow claim", block: func(h *authHarness, _ *fakeOIDCProvider, _ *fakeOIDCAccountStore) {
			h.sessionStore.beforeClaim = returnDeadlineExceeded
		}},
		{name: "token and verification", block: func(_ *authHarness, provider *fakeOIDCProvider, _ *fakeOIDCAccountStore) {
			provider.beforeExchange = returnDeadlineExceeded
		}},
		{name: "account reconciliation", block: func(_ *authHarness, _ *fakeOIDCProvider, accounts *fakeOIDCAccountStore) {
			accounts.beforeSignIn = returnDeadlineExceeded
		}},
		{name: "session commit", block: func(h *authHarness, _ *fakeOIDCProvider, _ *fakeOIDCAccountStore) {
			h.sessionStore.beforeCommit = returnDeadlineExceeded
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h, provider, accounts := newOIDCHarness(t, nil)
			cookie, state := startOIDC(t, h, "/")
			testCase.block(&h, provider, accounts)
			response := callbackOIDC(t, h, cookie, state)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("callback status = %d body %s", response.Code, response.Body.String())
			}
		})
	}
}

func returnDeadlineExceeded(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("callback phase has no deadline")
	}
	return context.DeadlineExceeded
}

func TestAuthProviders(t *testing.T) {
	disabled := newAuthHarness(t, nil)
	recorder := disabled.request(t, http.MethodGet, "/api/v1/auth/providers", "", nil)
	if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "oidc") {
		t.Fatalf("disabled providers = %d %s", recorder.Code, recorder.Body.String())
	}
	enabled, _, _ := newOIDCHarness(t, nil)
	recorder = enabled.request(t, http.MethodGet, "/api/v1/auth/providers", "", nil)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"display_name":"Company SSO"`) {
		t.Fatalf("enabled providers = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestOIDCOpenAPIContractCases(t *testing.T) {
	document := loadOpenAPI(t)
	for operation, responses := range oidcResponseContractTable() {
		assertOperationContract(t, document, operation, responses)
	}
	h, _, _ := newOIDCHarness(t, nil)
	providers := h.request(t, http.MethodGet, "/api/v1/auth/providers", "", nil)
	assertHandlerContract(t, document, providers, authContractCase{
		path: "/api/v1/auth/providers", method: "get", status: 200,
		schema: providersSchema, headers: []string{"Cache-Control", "X-Request-ID"},
	})
	cookie, state := startOIDC(t, h, "/")
	start := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", "", nil, "application/x-www-form-urlencoded")
	assertHandlerContract(t, document, start, authContractCase{
		path: "/api/v1/auth/oidc/start", method: "post", status: 303,
		headers: []string{"Cache-Control", "Location", "Set-Cookie", "Vary", "X-Request-ID"},
	})
	unexpected := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", "unexpected=value", nil,
		"application/x-www-form-urlencoded")
	assertHandlerContract(t, document, unexpected, authContractCase{
		path: "/api/v1/auth/oidc/start", method: "post", status: 422, schema: errorSchema,
		headers: []string{"Cache-Control", "Vary", "X-Request-ID"},
	})
	callback := callbackOIDC(t, h, cookie, state)
	assertHandlerContract(t, document, callback, authContractCase{
		path: "/api/v1/auth/oidc/callback", method: "get", status: 303,
		headers: []string{"Cache-Control", "Location", "Set-Cookie", "Vary", "X-Request-ID"},
	})
	h, _, _ = newOIDCHarness(t, nil)
	cookie, _ = startOIDC(t, h, "/")
	rejected := callbackOIDC(t, h, cookie, "wrong")
	assertHandlerContract(t, document, rejected, authContractCase{
		path: "/api/v1/auth/oidc/callback", method: "get", status: 401, schema: errorSchema,
		headers: []string{"Cache-Control", "Vary", "X-Request-ID"},
	})
}

func TestOIDCStartOpenAPIRequestBodyContract(t *testing.T) {
	document := loadOpenAPI(t)
	operation := document.validator.Paths.Find("/api/v1/auth/oidc/start").Post
	if operation.RequestBody == nil || operation.RequestBody.Value == nil {
		t.Fatal("OIDC start request body is not documented")
	}
	if operation.RequestBody.Value.Required {
		t.Fatal("OIDC start request body is required, want optional")
	}
	mediaType := operation.RequestBody.Value.Content.Get("application/x-www-form-urlencoded")
	if mediaType == nil || mediaType.Schema == nil || mediaType.Schema.Value == nil {
		t.Fatal("OIDC start form schema is missing")
	}
	for _, body := range []map[string]any{{}, {"return_to": "/requests"}} {
		if err := mediaType.Schema.Value.VisitJSON(body); err != nil {
			t.Errorf("OIDC start schema rejected body %#v: %v", body, err)
		}
	}
	if err := mediaType.Schema.Value.VisitJSON(map[string]any{"unexpected": "value"}); err == nil {
		t.Fatal("OIDC start schema accepted an unexpected form field")
	}
}

func oidcResponseContractTable() map[string]map[int]authContractCase {
	location := []string{"Cache-Control", "Location", "Set-Cookie", "Vary", "X-Request-ID"}
	session := []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"}
	sessionNoCookie := []string{"Cache-Control", "Vary", "X-Request-ID"}
	methodGet := []string{"Allow", "Cache-Control", "X-Request-ID"}
	return map[string]map[int]authContractCase{
		"get /api/v1/auth/providers": {
			200: {schema: providersSchema, headers: []string{"Cache-Control", "X-Request-ID"}},
			405: {schema: errorSchema, headers: methodGet},
		},
		"post /api/v1/auth/oidc/start": {
			303: {headers: location}, 400: {schema: errorSchema, headers: sessionNoCookie}, 401: {schema: errorSchema, headers: session},
			403: {schema: errorSchema, headers: []string{"Cache-Control", "X-Request-ID"}}, 404: {schema: errorSchema, headers: []string{"X-Request-ID"}}, 405: {schema: errorSchema, headers: []string{"Allow", "Cache-Control", "X-Request-ID"}},
			415: {schema: errorSchema, headers: sessionNoCookie},
			429: {schema: errorSchema, headers: []string{"Cache-Control", "Retry-After", "Vary", "X-Request-ID"}},
			422: {schema: errorSchema, headers: sessionNoCookie},
			500: {schema: errorSchema, headers: session},
		},
		"get /api/v1/auth/oidc/callback": {
			303: {headers: []string{"Cache-Control", "Location", "Retry-After", "Set-Cookie", "Vary", "X-Request-ID"}},
			401: {schema: errorSchema, headers: session},
			403: {schema: errorSchema, headers: session}, 404: {schema: errorSchema, headers: []string{"X-Request-ID"}},
			405: {schema: errorSchema, headers: methodGet},
			429: {schema: errorSchema, headers: []string{"Cache-Control", "Retry-After", "Vary", "X-Request-ID"}},
			500: {schema: errorSchema, headers: session},
			503: {schema: errorSchema, headers: []string{"Cache-Control", "Retry-After", "Set-Cookie", "Vary", "X-Request-ID"}},
		},
	}
}

func TestOIDCStartAndCallbackSuccess(t *testing.T) {
	h, provider, store := newOIDCHarness(t, nil)
	cookie, state := startOIDC(t, h, "/dashboard?tab=one#section")
	if strings.Contains(cookie.Value, state) {
		t.Fatal("session cookie exposed OIDC state")
	}
	recorder := callbackOIDC(t, h, cookie, state)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/dashboard?tab=one#section" {
		t.Fatalf("callback = %d location %q body %s", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String())
	}
	if provider.code != "code-1" || provider.verifier == "" || provider.nonce == "" {
		t.Fatalf("provider exchange = code %q verifier %q nonce %q", provider.code, provider.verifier, provider.nonce)
	}
	if provider.challenge != oauth2.S256ChallengeFromVerifier(provider.verifier) {
		t.Fatalf("PKCE challenge = %q, want verifier-bound S256 challenge", provider.challenge)
	}
	if store.input.Provider != "oidc" || store.input.Issuer != "https://id.example" || store.input.Subject != "subject-1" ||
		strings.Join(store.input.MappedRoles, ",") != "member,owner" {
		t.Fatalf("store input = %+v", store.input)
	}
	event := h.audit.last(t)
	if event.Actor != store.result.Account.ID || event.Source != "192.0.2.10" || event.Provider != "oidc" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("audit = %+v", event)
	}
	if h.metrics.loginProvider() != "oidc" || h.metrics.loginOutcome() != "success" {
		t.Fatalf("metric = %q %q", h.metrics.loginProvider(), h.metrics.loginOutcome())
	}
	newCookie := sessionCookie(t, recorder)
	if newCookie.Value == cookie.Value {
		t.Fatal("callback did not replace the pre-authentication session token")
	}
	if oldSession := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie); oldSession.Code != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, want 401", oldSession.Code)
	}
	if newSession := h.request(t, http.MethodGet, "/api/v1/auth/me", "", newCookie); newSession.Code != http.StatusOK {
		t.Fatalf("new session status = %d body %s", newSession.Code, newSession.Body.String())
	}
}

func TestOIDCCallbackAuditsRoleReconciliation(t *testing.T) {
	h, _, store := newOIDCHarness(t, nil)
	store.result.AddedRoles = []string{"member", "owner"}
	store.result.RemovedRoles = []string{"former-role"}
	cookie, state := startOIDC(t, h, "/")
	recorder := callbackOIDC(t, h, cookie, state)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d body %s", recorder.Code, recorder.Body.String())
	}
	events := h.audit.snapshot()
	if len(events) != 4 {
		t.Fatalf("audit event count = %d, want 4: %+v", len(events), events)
	}
	assertOIDCRoleAudit(t, events[0], telemetry.AuditActionRoleAssign, "member", store.result.Account.ID)
	assertOIDCRoleAudit(t, events[1], telemetry.AuditActionRoleAssign, "owner", store.result.Account.ID)
	assertOIDCRoleAudit(t, events[2], telemetry.AuditActionRoleRemove, "former-role", store.result.Account.ID)
	if events[3].Action != "auth.login" || events[3].Result != telemetry.AuditSuccess {
		t.Fatalf("login audit = %+v", events[3])
	}
}

func TestOIDCCallbackWithoutRoleClaimRevokesOIDCRolesAndAuditsRemoval(t *testing.T) {
	h, _, store := newOIDCHarness(t, func(_ *config.AuthConfig, oidcCfg *config.OIDCConfig) {
		oidcCfg.RoleClaim = ""
		oidcCfg.RoleMap = nil
	})
	store.result.RemovedRoles = []string{"owner"}
	cookie, state := startOIDC(t, h, "/")
	if recorder := callbackOIDC(t, h, cookie, state); recorder.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d body %s", recorder.Code, recorder.Body.String())
	}
	if len(store.input.MappedRoles) != 0 {
		t.Fatalf("role reconciliation input = %+v, want empty mapped set", store.input)
	}
	events := h.audit.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit event count = %d, want removal plus login: %+v", len(events), events)
	}
	assertOIDCRoleAudit(t, events[0], telemetry.AuditActionRoleRemove, "owner", store.result.Account.ID)
}

func assertOIDCRoleAudit(t *testing.T, event telemetry.AuditEvent, action, role, accountID string) {
	t.Helper()
	if event.Actor != accountID || event.Action != action || event.Resource != accountResource(accountID) ||
		event.Role != role || event.Provider != telemetry.AuditProviderOIDC || event.Source != "192.0.2.10" ||
		event.RequestID != "request-1" {
		t.Fatalf("OIDC role audit = %+v", event)
	}
}

func TestOIDCProviderRejectionIsOpaque(t *testing.T) {
	h, provider, _ := newOIDCHarness(t, nil)
	cookie, state := startOIDC(t, h, "/")
	provider.err = &core.OIDCRejection{Stage: "token exchange", Err: errors.New("provider leaked token secret-value")}
	recorder := callbackOIDC(t, h, cookie, state)
	if recorder.Code != http.StatusUnauthorized || strings.Contains(recorder.Body.String(), "secret-value") || strings.Contains(h.logs.String(), "secret-value") {
		t.Fatalf("provider failure = %d body %q logs %q", recorder.Code, recorder.Body.String(), h.logs.String())
	}
}

func TestOIDCTokenExchangeFailureContract(t *testing.T) {
	tests := []struct {
		name, browserCode, reason, jsonCode string
		err                                 error
		status                              int
	}{
		{
			name: "invalid_grant", err: &core.OIDCRejection{Stage: "token exchange", Err: errors.New("detail")},
			status: http.StatusUnauthorized, jsonCode: "oidc_rejected", browserCode: "token_invalid", reason: "token_invalid",
		},
		{
			name: "invalid_client", err: errors.New("invalid_client detail"),
			status: http.StatusInternalServerError, jsonCode: "internal", browserCode: "internal_error", reason: "internal_error",
		},
		{
			name: "404", err: errors.New("token endpoint 404 detail"),
			status: http.StatusInternalServerError, jsonCode: "internal", browserCode: "internal_error", reason: "internal_error",
		},
		{
			name: "429", err: &core.OIDCDependencyError{Operation: "token_exchange", Err: errors.New("429 detail")},
			status: http.StatusServiceUnavailable, jsonCode: "unavailable", browserCode: "provider_unavailable", reason: "provider_unavailable",
		},
		{
			name: "503", err: &core.OIDCDependencyError{Operation: "token_exchange", Err: errors.New("503 detail")},
			status: http.StatusServiceUnavailable, jsonCode: "unavailable", browserCode: "provider_unavailable", reason: "provider_unavailable",
		},
		{
			name: "malformed response", err: errors.New("malformed token response detail"),
			status: http.StatusInternalServerError, jsonCode: "internal", browserCode: "internal_error", reason: "internal_error",
		},
		{
			name: "oversized response", err: errors.New("oversized token response detail"),
			status: http.StatusInternalServerError, jsonCode: "internal", browserCode: "internal_error", reason: "internal_error",
		},
		{
			name: "permanent transport", err: errors.New("permanent transport detail"),
			status: http.StatusInternalServerError, jsonCode: "internal", browserCode: "internal_error", reason: "internal_error",
		},
		{
			name: "redirect rejected", err: errors.New("redirect rejected detail"),
			status: http.StatusInternalServerError, jsonCode: "internal", browserCode: "internal_error", reason: "internal_error",
		},
		{
			name: "caller cancellation", err: context.Canceled,
			status: http.StatusInternalServerError, jsonCode: "internal", browserCode: "internal_error", reason: "internal_error",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			assertOIDCExchangeJSONFailure(t, testCase.err, testCase.status, testCase.jsonCode, testCase.reason)
			assertOIDCExchangeBrowserFailure(t, testCase.err, testCase.browserCode, testCase.reason)
		})
	}
}

func TestOIDCPermanentJWKSFailuresAreInternal(t *testing.T) {
	for _, name := range []string{"401", "404", "redirect rejected"} {
		t.Run(name, func(t *testing.T) {
			err := fmt.Errorf("JWKS %s: %w", name, errors.New("provider detail"))
			assertOIDCExchangeJSONFailure(t, err, http.StatusInternalServerError, codeInternal, "internal_error")
			assertOIDCExchangeBrowserFailure(t, err, string(oidcErrorInternal), "internal_error")
		})
	}
}

func assertOIDCExchangeJSONFailure(t *testing.T, exchangeErr error, status int, code, reason string) {
	t.Helper()
	h, provider, _ := newOIDCHarness(t, nil)
	provider.err = exchangeErr
	cookie, state := startOIDC(t, h, "/")
	response := callbackOIDC(t, h, cookie, state)
	if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) ||
		strings.Contains(response.Body.String(), "detail") {
		t.Fatalf("JSON failure = %d body %q, want code %q", response.Code, response.Body.String(), code)
	}
	assertOIDCFailureTelemetry(t, h, reason)
}

func assertOIDCExchangeBrowserFailure(t *testing.T, exchangeErr error, code, reason string) {
	t.Helper()
	h, provider, _ := newOIDCHarness(t, nil)
	provider.err = exchangeErr
	cookie, state := startOIDC(t, h, "/")
	response := callbackOIDCHTML(h, cookie, state)
	want := "https://bloom.example/login?error=" + code
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != want {
		t.Fatalf("browser failure = %d Location %q, want %q", response.Code, response.Header().Get("Location"), want)
	}
	assertOIDCFailureTelemetry(t, h, reason)
}

func assertOIDCFailureTelemetry(t *testing.T, h authHarness, reason string) {
	t.Helper()
	event := h.audit.last(t)
	if event.Action != "auth.login" || event.Result != telemetry.AuditFailure || event.Reason != reason ||
		event.Provider != telemetry.AuditProviderOIDC || h.metrics.loginOutcome() != reason {
		t.Fatalf("failure telemetry = audit %+v metric %q, want reason %q", event, h.metrics.loginOutcome(), reason)
	}
}

func TestOIDCStoreFailureIsOpaque(t *testing.T) {
	h, _, store := newOIDCHarness(t, nil)
	cookie, state := startOIDC(t, h, "/")
	store.err = errors.New("database unavailable")
	recorder := callbackOIDC(t, h, cookie, state)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "database unavailable") {
		t.Fatalf("store failure = %d body %q", recorder.Code, recorder.Body.String())
	}
}

func TestOIDCMissingRoleFailureIsOpaque(t *testing.T) {
	h, _, store := newOIDCHarness(t, nil)
	store.err = fmt.Errorf("assign configured role: %w", core.ErrNotFound)
	cookie, state := startOIDC(t, h, "/")
	jsonResponse := callbackOIDC(t, h, cookie, state)
	if jsonResponse.Code != http.StatusInternalServerError || strings.Contains(jsonResponse.Body.String(), "not found") {
		t.Fatalf("JSON missing role = %d body %q", jsonResponse.Code, jsonResponse.Body.String())
	}

	cookie, state = startOIDC(t, h, "/")
	path := "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&code=code-1"
	req := httptest.NewRequest(http.MethodGet, "https://bloom.test"+path, nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(cookie)
	browserResponse := httptest.NewRecorder()
	h.h.ServeHTTP(browserResponse, req)
	if browserResponse.Code != http.StatusSeeOther || browserResponse.Header().Get("Location") != "https://bloom.example/login?error=internal_error" {
		t.Fatalf("browser missing role = %d Location %q body %q", browserResponse.Code, browserResponse.Header().Get("Location"), browserResponse.Body.String())
	}
}

func TestOIDCSessionCommitFailureHasNoSuccessTelemetry(t *testing.T) {
	h, _, store := newOIDCHarness(t, nil)
	store.result.AddedRoles = []string{"owner"}
	store.result.RemovedRoles = []string{"member"}
	cookie, state := startOIDC(t, h, "/")
	h.sessionStore.mu.Lock()
	h.sessionStore.commitErr = errors.New("commit failed")
	h.sessionStore.mu.Unlock()
	recorder := callbackOIDC(t, h, cookie, state)
	if recorder.Code != http.StatusInternalServerError || h.metrics.loginOutcome() != "internal_error" {
		t.Fatalf("commit failure = %d metric %q body %q", recorder.Code, h.metrics.loginOutcome(), recorder.Body.String())
	}
	events := h.audit.snapshot()
	if len(events) != 3 {
		t.Fatalf("commit failure audit count = %d, want 3: %+v", len(events), events)
	}
	assertOIDCRoleAudit(t, events[0], telemetry.AuditActionRoleAssign, "owner", store.result.Account.ID)
	assertOIDCRoleAudit(t, events[1], telemetry.AuditActionRoleRemove, "member", store.result.Account.ID)
	if events[2].Action != "auth.login" || events[2].Result != telemetry.AuditFailure {
		t.Fatalf("commit failure login audit = %+v", events[2])
	}
}

func TestOIDCBrowserSessionCommitFailureRedirects(t *testing.T) {
	h, _, _ := newOIDCHarness(t, nil)
	cookie, state := startOIDC(t, h, "/")
	h.sessionStore.mu.Lock()
	h.sessionStore.commitErr = errors.New("commit secret detail")
	h.sessionStore.mu.Unlock()
	path := "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&code=code-1"
	req := httptest.NewRequest(http.MethodGet, "https://bloom.test"+path, nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	h.h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "https://bloom.example/login?error=internal_error" {
		t.Fatalf("commit failure = %d Location %q body %q", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String())
	}
}

func TestOIDCProviderErrorCallbackIsOpaque(t *testing.T) {
	h, _, _ := newOIDCHarness(t, nil)
	cookie, state := startOIDC(t, h, "/")
	path := "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&error=access_denied&error_description=provider-secret"
	recorder := h.request(t, http.MethodGet, path, "", cookie)
	if recorder.Code != http.StatusUnauthorized || strings.Contains(recorder.Body.String(), "provider-secret") || strings.Contains(h.logs.String(), "provider-secret") {
		t.Fatalf("provider callback = %d body %q logs %q", recorder.Code, recorder.Body.String(), h.logs.String())
	}
}

func TestOIDCStartRejectsGETAndCrossOriginPOST(t *testing.T) {
	h, _, _ := newOIDCHarness(t, nil)
	get := h.request(t, http.MethodGet, "/api/v1/auth/oidc/start", "", nil)
	if get.Code != http.StatusMethodNotAllowed || get.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET start = %d Allow %q", get.Code, get.Header().Get("Allow"))
	}
	crossOrigin := crossOriginRequest(h, http.MethodPost, "/api/v1/auth/oidc/start", "return_to=%2F")
	if crossOrigin.Code != http.StatusForbidden {
		t.Fatalf("cross-origin start = %d body %s", crossOrigin.Code, crossOrigin.Body.String())
	}
}

func TestOIDCDisabledRoutesAreNotMounted(t *testing.T) {
	h := newAuthHarness(t, nil)
	start := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", "", nil, "application/x-www-form-urlencoded")
	callback := h.request(t, http.MethodGet, "/api/v1/auth/oidc/callback", "", nil)
	if start.Code != http.StatusNotFound || callback.Code != http.StatusNotFound {
		t.Fatalf("disabled OIDC statuses = start %d callback %d", start.Code, callback.Code)
	}
}

func TestOIDCStartRequiresFormContentType(t *testing.T) {
	h, _, _ := newOIDCHarness(t, nil)
	recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", `{}`, nil, "application/json")
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("start content type status = %d body %s", recorder.Code, recorder.Body.String())
	}
}

func TestOIDCStartAcceptsOptionalBodyAndRejectsUnknownFields(t *testing.T) {
	h, _, _ := newOIDCHarness(t, nil)
	empty := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", "", nil, "")
	if empty.Code != http.StatusSeeOther {
		t.Fatalf("empty start body = %d body %s", empty.Code, empty.Body.String())
	}
	unknown := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start",
		"return_to=%2Frequests&unexpected=value", nil, "application/x-www-form-urlencoded")
	if unknown.Code != http.StatusUnprocessableEntity || !strings.Contains(unknown.Body.String(), `"field":"unexpected"`) {
		t.Fatalf("unknown start field = %d body %s", unknown.Code, unknown.Body.String())
	}
}

func TestOIDCStartReturnToValidationMatchesContract(t *testing.T) {
	document := loadOpenAPI(t)
	operation := document.validator.Paths.Find("/api/v1/auth/oidc/start").Post
	property := operation.RequestBody.Value.Content["application/x-www-form-urlencoded"].Schema.Value.Properties["return_to"].Value
	if property.MaxLength == nil || *property.MaxLength != maxReturnPathCharacters {
		t.Fatalf("return_to maxLength = %v, want %d", property.MaxLength, maxReturnPathCharacters)
	}

	h, _, _ := newOIDCHarness(t, nil)
	boundary := "/" + strings.Repeat("a", maxReturnPathCharacters-1)
	accepted := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start",
		url.Values{"return_to": {boundary}}.Encode(), nil, "application/x-www-form-urlencoded")
	if accepted.Code != http.StatusSeeOther {
		t.Fatalf("boundary return_to = %d body %s", accepted.Code, accepted.Body.String())
	}

	tests := []struct {
		name, body, fieldCode string
	}{
		{name: "over limit", body: url.Values{"return_to": {boundary + "a"}}.Encode(), fieldCode: "too_long"},
		{name: "duplicate", body: "return_to=%2Fone&return_to=%2Ftwo", fieldCode: "duplicate"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			response := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start",
				testCase.body, nil, "application/x-www-form-urlencoded")
			assertHandlerContract(t, document, response, authContractCase{
				path: "/api/v1/auth/oidc/start", method: "post", status: 422, schema: errorSchema,
				headers: []string{"Cache-Control", "Vary", "X-Request-ID"},
			})
			if !strings.Contains(response.Body.String(), `"field":"return_to"`) ||
				!strings.Contains(response.Body.String(), `"code":"`+testCase.fieldCode+`"`) {
				t.Fatalf("return_to failure = %d body %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestOIDCStartIsRateLimitedBeforeCreatingAnotherFlow(t *testing.T) {
	h, _, _ := newOIDCHarness(t, func(authCfg *config.AuthConfig, _ *config.OIDCConfig) {
		authCfg.LoginRateBurst = 1
	})
	first := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", "", nil, "application/x-www-form-urlencoded")
	second := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", "", nil, "application/x-www-form-urlencoded")
	if first.Code != http.StatusSeeOther || second.Code != http.StatusTooManyRequests ||
		second.Header().Get("Retry-After") == "" || second.Header().Get("Set-Cookie") != "" {
		t.Fatalf("start responses = %d and %d, Retry-After %q Set-Cookie %q",
			first.Code, second.Code, second.Header().Get("Retry-After"), second.Header().Get("Set-Cookie"))
	}
}

func TestOIDCCallbackBulkheadRejectsExcessWork(t *testing.T) {
	h, provider, _ := newOIDCHarness(t, nil)
	provider.exchangeStarted = make(chan struct{}, oidcMaxConcurrentExchanges)
	provider.exchangeRelease = make(chan struct{})
	flows := make([]struct {
		cookie *http.Cookie
		state  string
	}, oidcMaxConcurrentExchanges+1)
	for index := range flows {
		flows[index].cookie, flows[index].state = startOIDC(t, h, "/")
	}
	done := make(chan *httptest.ResponseRecorder, oidcMaxConcurrentExchanges)
	for index := range oidcMaxConcurrentExchanges {
		go func() { done <- callbackOIDC(t, h, flows[index].cookie, flows[index].state) }()
	}
	for range oidcMaxConcurrentExchanges {
		<-provider.exchangeStarted
	}
	overload := callbackOIDC(t, h, flows[oidcMaxConcurrentExchanges].cookie, flows[oidcMaxConcurrentExchanges].state)
	if overload.Code != http.StatusServiceUnavailable || overload.Header().Get("Retry-After") != "1" {
		t.Fatalf("bulkhead response = %d Retry-After %q body %s", overload.Code, overload.Header().Get("Retry-After"), overload.Body.String())
	}
	close(provider.exchangeRelease)
	for range oidcMaxConcurrentExchanges {
		if recorder := <-done; recorder.Code != http.StatusSeeOther {
			t.Fatalf("admitted callback = %d body %s", recorder.Code, recorder.Body.String())
		}
	}
}

func TestOIDCCallbackClaimsFlowBeforeExchange(t *testing.T) {
	h, provider, _ := newOIDCHarness(t, nil)
	provider.exchangeStarted = make(chan struct{})
	provider.exchangeRelease = make(chan struct{})
	cookie, state := startOIDC(t, h, "/")
	loaded := make(chan struct{}, 2)
	releaseLoads := make(chan struct{})
	h.sessionStore.mu.Lock()
	h.sessionStore.findReady = loaded
	h.sessionStore.findRelease = releaseLoads
	h.sessionStore.mu.Unlock()
	done := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { done <- callbackOIDCHTML(h, cookie, state) }()
	}
	<-loaded
	<-loaded
	close(releaseLoads)
	<-provider.exchangeStarted
	select {
	case <-provider.exchangeStarted:
		close(provider.exchangeRelease)
		<-done
		<-done
		t.Fatal("concurrent callback reached token exchange")
	case second := <-done:
		if second.Code != http.StatusSeeOther || second.Header().Get("Location") != "https://bloom.example/login?error=state_invalid" {
			t.Fatalf("second callback = %d Location %q body %s, want state_invalid rejection",
				second.Code, second.Header().Get("Location"), second.Body.String())
		}
	}
	close(provider.exchangeRelease)
	if first := <-done; first.Code != http.StatusSeeOther || first.Header().Get("Location") != "/" {
		t.Fatalf("first callback = %d body %s", first.Code, first.Body.String())
	}
	if got := provider.exchanges(); got != 1 {
		t.Fatalf("token exchanges = %d, want 1", got)
	}
}

func TestOIDCCallbackLosingResponseCannotReplaceAuthenticatedCookie(t *testing.T) {
	h, _, _ := newOIDCHarness(t, nil)
	cookie, state := startOIDC(t, h, "/")
	loaded := make(chan struct{}, 2)
	releaseLoads := make(chan struct{})
	claimReady := make(chan int, 2)
	claimRelease := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var claimMu sync.Mutex
	claimIndex := 0
	h.sessionStore.mu.Lock()
	h.sessionStore.findReady = loaded
	h.sessionStore.findRelease = releaseLoads
	h.sessionStore.beforeClaim = func(ctx context.Context) error {
		claimMu.Lock()
		index := claimIndex
		claimIndex++
		claimMu.Unlock()
		claimReady <- index
		select {
		case <-claimRelease[index]:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	h.sessionStore.mu.Unlock()
	done := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { done <- callbackOIDCHTML(h, cookie, state) }()
	}
	<-loaded
	<-loaded
	close(releaseLoads)
	firstClaim, secondClaim := <-claimReady, <-claimReady
	close(claimRelease[firstClaim])
	winner := <-done
	if winner.Code != http.StatusSeeOther || winner.Header().Get("Location") != "/" {
		t.Fatalf("winning callback = %d Location %q", winner.Code, winner.Header().Get("Location"))
	}
	winnerCookie := sessionCookie(t, winner)
	close(claimRelease[secondClaim])
	loser := <-done
	if loser.Header().Get("Set-Cookie") != "" {
		t.Fatalf("losing callback Set-Cookie = %q, want none", loser.Header().Get("Set-Cookie"))
	}
	if loser.Code != http.StatusSeeOther || loser.Header().Get("Location") != "https://bloom.example/login?error=state_invalid" {
		t.Fatalf("losing callback = %d Location %q", loser.Code, loser.Header().Get("Location"))
	}
	if authenticated := h.request(t, http.MethodGet, "/api/v1/auth/me", "", winnerCookie); authenticated.Code != http.StatusOK {
		t.Fatalf("winning session after losing response = %d body %s", authenticated.Code, authenticated.Body.String())
	}
}

func TestOIDCCallbackFlowClaimFailureIsOpaque(t *testing.T) {
	h, provider, _ := newOIDCHarness(t, nil)
	cookie, state := startOIDC(t, h, "/")
	h.sessionStore.mu.Lock()
	h.sessionStore.claimErr = errors.New("database secret detail")
	h.sessionStore.mu.Unlock()
	response := callbackOIDC(t, h, cookie, state)
	if response.Code != http.StatusInternalServerError || provider.exchanges() != 0 ||
		strings.Contains(response.Body.String(), "database secret detail") {
		t.Fatalf("claim failure = %d after %d exchanges: %s", response.Code, provider.exchanges(), response.Body.String())
	}
}

func callbackOIDCHTML(h authHarness, cookie *http.Cookie, state string) *httptest.ResponseRecorder {
	path := "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&code=code-1"
	request := httptest.NewRequest(http.MethodGet, "https://bloom.test"+path, nil)
	request.Header.Set("Accept", "text/html")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	h.h.ServeHTTP(response, request)
	return response
}

func TestOIDCCallbackRejectsOversizedAndRepeatedParameters(t *testing.T) {
	tests := []struct {
		name  string
		query func(string) string
	}{
		{name: "repeated state", query: func(state string) string { return "state=" + state + "&state=other&code=ok" }},
		{name: "oversized state", query: func(string) string { return "state=" + strings.Repeat("s", maxOIDCCallbackStateBytes+1) + "&code=ok" }},
		{name: "repeated code", query: func(state string) string { return "state=" + state + "&code=one&code=two" }},
		{name: "oversized code", query: func(state string) string {
			return "state=" + state + "&code=" + strings.Repeat("c", maxOIDCCallbackCodeBytes+1)
		}},
		{name: "repeated error", query: func(state string) string { return "state=" + state + "&error=one&error=two" }},
		{name: "oversized error", query: func(state string) string {
			return "state=" + state + "&error=" + strings.Repeat("e", maxOIDCCallbackErrorBytes+1)
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h, _, _ := newOIDCHarness(t, nil)
			cookie, state := startOIDC(t, h, "/")
			recorder := h.request(t, http.MethodGet, "/api/v1/auth/oidc/callback?"+testCase.query(state), "", cookie)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("callback = %d body %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestOIDCCallbackOpenAPIParameterBounds(t *testing.T) {
	operation := loadOpenAPI(t).validator.Paths.Find("/api/v1/auth/oidc/callback").Get
	want := map[string]uint64{
		"state": maxOIDCCallbackStateBytes,
		"code":  maxOIDCCallbackCodeBytes,
		"error": maxOIDCCallbackErrorBytes,
	}
	for _, parameterRef := range operation.Parameters {
		parameter := parameterRef.Value
		limit, ok := want[parameter.Name]
		if !ok {
			continue
		}
		if parameter.Schema == nil || parameter.Schema.Value == nil || parameter.Schema.Value.MaxLength == nil ||
			*parameter.Schema.Value.MaxLength != limit || parameter.Schema.Value.Pattern == "" ||
			!strings.Contains(parameter.Description, "exactly once") {
			t.Fatalf("parameter %q does not document max %d ASCII characters and single-value use", parameter.Name, limit)
		}
		if err := parameter.Schema.Value.VisitJSON("é"); err == nil {
			t.Fatalf("parameter %q schema accepted a multibyte value", parameter.Name)
		}
		delete(want, parameter.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing callback parameter constraints: %v", want)
	}
}

func TestOIDCCallbackRejectsMultibyteParameters(t *testing.T) {
	for _, query := range []string{"state=%C3%A9&code=ok", "state=state&code=%C3%A9", "state=state&error=%C3%A9"} {
		h, provider, _ := newOIDCHarness(t, nil)
		cookie, _ := startOIDC(t, h, "/")
		response := h.request(t, http.MethodGet, "/api/v1/auth/oidc/callback?"+query, "", cookie)
		if response.Code != http.StatusUnauthorized || provider.exchanges() != 0 {
			t.Fatalf("query %q = %d after %d exchanges, want rejection before exchange", query, response.Code, provider.exchanges())
		}
	}
}

func TestOIDCStartRejectsMalformedSession(t *testing.T) {
	h, _, _ := newOIDCHarness(t, nil)
	token := "malformed-oidc-start-session"
	if err := h.sessions.Store.Commit(hashedSessionToken(token), []byte("not-session-data"), futureSessionInstant()); err != nil {
		t.Fatalf("seed malformed session: %v", err)
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: token}
	recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/oidc/start", "", cookie, "application/x-www-form-urlencoded")
	if recorder.Code != http.StatusUnauthorized || !strings.Contains(recorder.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("malformed session = %d body %s", recorder.Code, recorder.Body.String())
	}
}

func TestOIDCBrowserFailuresRedirectToClosedCodes(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*fakeOIDCProvider, *fakeOIDCAccountStore)
		state   func(string) string
		want    oidcBrowserError
	}{
		{name: "state", state: func(string) string { return "wrong" }, want: oidcErrorStateInvalid},
		{name: "token", prepare: func(p *fakeOIDCProvider, _ *fakeOIDCAccountStore) { p.err = core.ErrOIDCRejected }, want: oidcErrorTokenInvalid},
		{name: "provider", prepare: func(p *fakeOIDCProvider, _ *fakeOIDCAccountStore) { p.err = core.ErrOIDCProviderUnavailable }, want: oidcErrorProviderUnavailable},
		{name: "provisioning", prepare: func(_ *fakeOIDCProvider, s *fakeOIDCAccountStore) { s.err = core.ErrOIDCProvisioningDisabled }, want: oidcErrorProvisioningDisabled},
		{name: "unknown", prepare: func(_ *fakeOIDCProvider, s *fakeOIDCAccountStore) { s.err = core.ErrForbidden }, want: oidcErrorUnknownIdentity},
		{name: "disabled", prepare: func(_ *fakeOIDCProvider, s *fakeOIDCAccountStore) {
			s.err = &core.CredentialFailure{Reason: core.CredentialDisabled}
		}, want: oidcErrorDisabled},
		{name: "internal", prepare: func(_ *fakeOIDCProvider, s *fakeOIDCAccountStore) { s.err = errors.New("database secret detail") }, want: oidcErrorInternal},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h, provider, store := newOIDCHarness(t, nil)
			cookie, state := startOIDC(t, h, "/")
			if testCase.prepare != nil {
				testCase.prepare(provider, store)
			}
			if testCase.state != nil {
				state = testCase.state(state)
			}
			path := "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&code=code-1"
			req := httptest.NewRequest(http.MethodGet, "https://bloom.test"+path, nil)
			req.RemoteAddr = "192.0.2.10:4321"
			req.Header.Set("Accept", "text/html,application/xhtml+xml")
			req.AddCookie(cookie)
			recorder := httptest.NewRecorder()
			h.h.ServeHTTP(recorder, req)
			want := "https://bloom.example/login?error=" + string(testCase.want)
			if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != want || strings.Contains(recorder.Header().Get("Location"), "secret") {
				t.Fatalf("browser failure = %d Location %q body %s", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String())
			}
		})
	}
}

func TestOIDCBrowserMalformedSessionRedirectsWithoutDetail(t *testing.T) {
	h, _, _ := newOIDCHarness(t, nil)
	token := "malformed-oidc-session"
	if err := h.sessions.Store.Commit(hashedSessionToken(token), []byte("not-session-data"), futureSessionInstant()); err != nil {
		t.Fatalf("seed malformed session: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "https://bloom.test/api/v1/auth/oidc/callback?state=x&code=y", nil)
	req.Header.Set("Accept", "text/html")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	recorder := httptest.NewRecorder()
	h.h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "https://bloom.example/login?error=state_invalid" {
		t.Fatalf("malformed session = %d Location %q body %s", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String())
	}
}

func TestOIDCProviderUnavailableReturnsOpaque503(t *testing.T) {
	h, provider, _ := newOIDCHarness(t, nil)
	cookie, state := startOIDC(t, h, "/")
	provider.err = &core.OIDCDependencyError{Operation: "jwks", Err: errors.New("provider detail")}
	recorder := callbackOIDC(t, h, cookie, state)
	if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Retry-After") == "" ||
		strings.Contains(recorder.Body.String(), "provider detail") || h.metrics.loginOutcome() != "provider_unavailable" {
		t.Fatalf("unavailable = %d headers %v metric %q body %q", recorder.Code, recorder.Header(), h.metrics.loginOutcome(), recorder.Body.String())
	}
}

func FuzzSafeReturnPath(f *testing.F) {
	for _, seed := range []string{
		"", "/", "/requests?q=one#two", "https://evil.example", "//evil.example", "/login",
		"/" + strings.Repeat("é", 1023),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := safeReturnPath(raw, "https://bloom.example")
		if len(got) > maxReturnLocationBytes || !strings.HasPrefix(got, "/") || strings.HasPrefix(got, "//") {
			t.Fatalf("unsafe bounded result %q from %q", got, raw)
		}
		parsed, err := url.Parse(got)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || isLoginPath(parsed.Path) {
			t.Fatalf("unsafe result %q from %q: %v", got, raw, err)
		}
	})
}

func FuzzDecodeOIDCCallbackParams(f *testing.F) {
	seeds := []string{
		"state=state&code=code", "state=%", "state=%ZZ", "state=%FF", "state=one&state=two",
		"state=" + strings.Repeat("s", maxOIDCCallbackStateBytes),
		"state=" + strings.Repeat("s", maxOIDCCallbackStateBytes+1),
		"state=state&code=" + strings.Repeat("c", maxOIDCCallbackCodeBytes),
		"state=state&error=" + strings.Repeat("e", maxOIDCCallbackErrorBytes+1),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		params, invalid := decodeOIDCCallbackParams(raw)
		query, parseErr := url.ParseQuery(raw)
		if parseErr != nil && invalid == "" {
			t.Fatalf("accepted malformed query %q", raw)
		}
		if invalid != "" {
			return
		}
		values := map[string]struct {
			value string
			limit int
		}{
			"state": {value: params.state, limit: maxOIDCCallbackStateBytes},
			"code":  {value: params.code, limit: maxOIDCCallbackCodeBytes},
			"error": {value: params.providerError, limit: maxOIDCCallbackErrorBytes},
		}
		for name, checked := range values {
			if !asciiString(checked.value) || len(checked.value) > checked.limit || len(query[name]) > 1 {
				t.Fatalf("accepted invalid %s from %q", name, raw)
			}
		}
	})
}

func TestOIDCStateMismatchPreservesFlowAndExpiryRejects(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(authHarness, string) string
		replayCode int
	}{
		{name: "mismatch", prepare: func(_ authHarness, _ string) string { return "wrong" }, replayCode: http.StatusSeeOther},
		{name: "expired", prepare: func(h authHarness, state string) string {
			h.clock.Advance(oidcFlowLifetime)
			return state
		}, replayCode: http.StatusUnauthorized},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h, _, _ := newOIDCHarness(t, nil)
			cookie, state := startOIDC(t, h, "/")
			recorder := callbackOIDC(t, h, cookie, testCase.prepare(h, state))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("callback status = %d body %s", recorder.Code, recorder.Body.String())
			}
			replayCookie := cookie
			if len(recorder.Result().Cookies()) > 0 {
				replayCookie = recorder.Result().Cookies()[0]
			}
			replay := callbackOIDC(t, h, replayCookie, state)
			if replay.Code != testCase.replayCode {
				t.Fatalf("replay status = %d body %s, want %d", replay.Code, replay.Body.String(), testCase.replayCode)
			}
		})
	}
}

func TestOIDCProvisioningDisabledAndRateLimited(t *testing.T) {
	h, _, store := newOIDCHarness(t, func(authCfg *config.AuthConfig, oidcCfg *config.OIDCConfig) {
		authCfg.LoginRateBurst = 1
		oidcCfg.DefaultRole = ""
	})
	store.err = core.ErrForbidden
	cookie, state := startOIDC(t, h, "/")
	denied := callbackOIDC(t, h, cookie, state)
	if denied.Code != http.StatusForbidden || h.audit.last(t).Actor != "anonymous" {
		t.Fatalf("disabled provisioning = %d audit %+v", denied.Code, h.audit.last(t))
	}
	limited := callbackOIDC(t, h, cookie, state)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("limited callback = %d headers %v", limited.Code, limited.Header())
	}
}

func TestOIDCBrowserRateLimitRedirects(t *testing.T) {
	h, _, store := newOIDCHarness(t, func(authCfg *config.AuthConfig, _ *config.OIDCConfig) {
		authCfg.LoginRateBurst = 1
	})
	store.err = core.ErrForbidden
	cookie, state := startOIDC(t, h, "/")
	_ = callbackOIDC(t, h, cookie, state)
	path := "/api/v1/auth/oidc/callback?state=" + url.QueryEscape(state) + "&code=code-1"
	req := httptest.NewRequest(http.MethodGet, "https://bloom.test"+path, nil)
	req.RemoteAddr = "192.0.2.10:4321"
	req.Header.Set("Accept", "text/html")
	req.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	h.h.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "https://bloom.example/login?error=provider_unavailable" {
		t.Fatalf("rate limit = %d Location %q body %q", recorder.Code, recorder.Header().Get("Location"), recorder.Body.String())
	}
}

func TestSafeReturnPath(t *testing.T) {
	t.Parallel()
	for _, unsafe := range []string{"https://evil.example/", "//evil.example/", "/\\evil", "/login/", "/line\nbreak"} {
		if got := safeReturnPath(unsafe, "https://bloom.example"); got != "/" {
			t.Errorf("safeReturnPath(%q) = %q, want /", unsafe, got)
		}
	}
	if got := safeReturnPath("/requests?q=one#two", "https://bloom.example"); got != "/requests?q=one#two" {
		t.Fatalf("safe return path = %q", got)
	}
	multibyte := "/" + strings.Repeat("é", 1023)
	if got := safeReturnPath(multibyte, "https://bloom.example"); got == "/" || len(got) <= maxReturnPathCharacters {
		t.Fatalf("multibyte return path encoded length = %d, want accepted path over %d bytes", len(got), maxReturnPathCharacters)
	}
	tooLongEncoded := "/" + strings.Repeat("é", maxReturnPathCharacters-1)
	if got := safeReturnPath(tooLongEncoded, "https://bloom.example"); got != "/" {
		t.Fatalf("oversized encoded return path length = %d, want fallback", len(got))
	}
}
