package http

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/alexedwards/scs/v2/memstore"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
	"github.com/BonzTM/bloom/internal/testutil"
)

type authAccountStore struct {
	mu               sync.Mutex
	accounts         map[string]core.Account
	err              error
	blockAccountLoad bool
	deadlines        chan time.Duration
}

func (s *authAccountStore) CreateAccount(_ context.Context, account core.Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.accounts[account.Username]; exists {
		return core.ErrAlreadyExists
	}
	s.accounts[account.Username] = account
	return nil
}

func (s *authAccountStore) GetAccount(ctx context.Context, id string) (core.Account, error) {
	s.mu.Lock()
	blocked := s.blockAccountLoad
	deadlines := s.deadlines
	s.mu.Unlock()
	if blocked {
		if deadline, ok := ctx.Deadline(); ok {
			deadlines <- time.Until(deadline)
		}
		<-ctx.Done()
		return core.Account{}, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return core.Account{}, s.err
	}
	for _, account := range s.accounts {
		if account.ID == id {
			return account, nil
		}
	}
	return core.Account{}, core.ErrNotFound
}

func (s *authAccountStore) GetAccountByUsername(_ context.Context, username string) (core.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return core.Account{}, s.err
	}
	account, ok := s.accounts[username]
	if !ok {
		return core.Account{}, core.ErrNotFound
	}
	return account, nil
}

func (s *authAccountStore) UpdateAccountPasswordHash(_ context.Context, id, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for username, account := range s.accounts {
		if account.ID == id {
			account.PasswordHash = &hash
			s.accounts[username] = account
			return nil
		}
	}
	return core.ErrNotFound
}

func (s *authAccountStore) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *authAccountStore) blockLoads(deadlines chan time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockAccountLoad = true
	s.deadlines = deadlines
}

func (s *authAccountStore) disable(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account := s.accounts[username]
	account.Disabled = true
	s.accounts[username] = account
}

func (s *authAccountStore) delete(username string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accounts, username)
}

type recordingAudit struct {
	mu     sync.Mutex
	events []telemetry.AuditEvent
	err    error
}

func (a *recordingAudit) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = nil
}

func (a *recordingAudit) snapshot() []telemetry.AuditEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.events)
}

type authAuthorization struct {
	mu                      sync.Mutex
	permissions             map[string][]core.Permission
	roleNames               map[string][]string
	roles                   []core.Role
	permissionsErr          error
	snapshotErr             error
	listRolesErr            error
	permissionsCalls        int
	snapshotCalls           int
	listRolesCalls          int
	advanceAfterPermissions bool
	beforeSnapshot          func(context.Context, string)
	lastAfterName           string
	lastPageSize            int
}

func (a *authAuthorization) Permissions(_ context.Context, accountID string) ([]core.Permission, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permissionsCalls++
	if a.permissionsErr != nil {
		return nil, a.permissionsErr
	}
	permissions := slices.Clone(a.permissions[accountID])
	if a.advanceAfterPermissions {
		a.permissions[accountID] = []core.Permission{core.PermissionRequestsCreate}
		a.roleNames[accountID] = []string{"member"}
	}
	return permissions, nil
}

func (a *authAuthorization) Snapshot(ctx context.Context, accountID string) (core.AuthorizationSnapshot, error) {
	a.mu.Lock()
	a.snapshotCalls++
	beforeSnapshot := a.beforeSnapshot
	a.mu.Unlock()
	if beforeSnapshot != nil {
		beforeSnapshot(ctx, accountID)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.snapshotErr != nil {
		return core.AuthorizationSnapshot{}, a.snapshotErr
	}
	return core.AuthorizationSnapshot{
		RoleNames: slices.Clone(a.roleNames[accountID]), Permissions: slices.Clone(a.permissions[accountID]),
	}, nil
}

func (a *authAuthorization) ListRoles(_ context.Context, afterName string, pageSize int) ([]core.Role, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.listRolesCalls++
	if a.listRolesErr != nil {
		return nil, a.listRolesErr
	}
	a.lastAfterName = afterName
	a.lastPageSize = pageSize
	roles := make([]core.Role, 0, pageSize)
	for _, role := range a.roles {
		if role.Name > afterName && len(roles) < pageSize {
			roles = append(roles, role)
		}
	}
	return roles, nil
}

func (a *authAuthorization) resetCalls() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permissionsCalls = 0
	a.snapshotCalls = 0
	a.listRolesCalls = 0
}

func (a *authAuthorization) RoleExists(_ context.Context, name string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, role := range a.roles {
		if role.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (a *recordingAudit) Emit(_ context.Context, event telemetry.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	return a.err
}

func (a *recordingAudit) last(t *testing.T) telemetry.AuditEvent {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.events) == 0 {
		t.Fatal("no audit event emitted")
	}
	return a.events[len(a.events)-1]
}

type authHarness struct {
	server        *Server
	h             http.Handler
	store         *authAccountStore
	sessions      *scs.SessionManager
	audit         *recordingAudit
	metrics       *countingMetrics
	clock         *testutil.FakeClock
	logs          *strings.Builder
	sessionStore  *controllableSessionStore
	authorization *authAuthorization
	mediaServers  *fakeMediaServerService
	invites       *fakeInviteService
	playback      *fakePlaybackReader
}

type controllableSessionStore struct {
	mu                            sync.Mutex
	base                          scs.Store
	findErr, commitErr, deleteErr error
	claimErr                      error
	deleteAfterErr                error
	beforeClaim                   func(context.Context) error
	deleted                       []string
	beforeCommit                  func(context.Context) error
	findReady                     chan<- struct{}
	findRelease                   <-chan struct{}
}

func (s *controllableSessionStore) Find(token string) ([]byte, bool, error) {
	return s.FindCtx(context.Background(), token)
}

func (s *controllableSessionStore) Commit(token string, data []byte, expiry time.Time) error {
	return s.CommitCtx(context.Background(), token, data, expiry)
}

func (s *controllableSessionStore) Delete(token string) error {
	return s.DeleteCtx(context.Background(), token)
}

func (s *controllableSessionStore) FindCtx(ctx context.Context, token string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	if s.findErr != nil {
		err := s.findErr
		s.mu.Unlock()
		return nil, false, err
	}
	data, found, err := s.base.Find(token)
	ready, release := s.findReady, s.findRelease
	s.mu.Unlock()
	if ready != nil {
		ready <- struct{}{}
		<-release
	}
	return data, found, err
}

func (s *controllableSessionStore) CommitCtx(ctx context.Context, token string, data []byte, expiry time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	beforeCommit := s.beforeCommit
	commitErr := s.commitErr
	s.mu.Unlock()
	if beforeCommit != nil {
		if err := beforeCommit(ctx); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if commitErr != nil {
		return commitErr
	}
	return s.base.Commit(token, data, expiry)
}

func (s *controllableSessionStore) DeleteCtx(ctx context.Context, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, token)
	if err := s.base.Delete(token); err != nil {
		return err
	}
	return s.deleteAfterErr
}

func (s *controllableSessionStore) ClaimOIDCFlow(ctx context.Context, token string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	beforeClaim := s.beforeClaim
	s.mu.Unlock()
	if beforeClaim != nil {
		if err := beforeClaim(ctx); err != nil {
			return false, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.claimErr != nil {
		return false, s.claimErr
	}
	storedToken := hashedSessionToken(token)
	_, found, err := s.base.Find(storedToken)
	if err != nil || !found {
		return false, err
	}
	if err := s.base.Delete(storedToken); err != nil {
		return false, err
	}
	return true, nil
}

func newAuthHarness(t *testing.T, mutate func(*config.AuthConfig)) authHarness {
	t.Helper()
	return newAuthHarnessWithSessionStore(t, mutate, nil)
}

func newAuthHarnessWithSessionStore(t *testing.T, mutate func(*config.AuthConfig), sessionStore *controllableSessionStore) authHarness {
	t.Helper()
	return newAuthHarnessConfigured(t, mutate, sessionStore, nil, nil)
}

func newAuthHarnessWithWeb(t *testing.T, web http.Handler) authHarness {
	t.Helper()
	return newAuthHarnessConfigured(t, nil, nil, web, nil)
}

func newAuthHarnessConfigured(
	t *testing.T,
	mutate func(*config.AuthConfig),
	sessionStore *controllableSessionStore,
	web http.Handler,
	identity core.IdentityProvider,
) authHarness {
	t.Helper()
	hash, err := core.HashPassword("secret-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	store := &authAccountStore{accounts: map[string]core.Account{
		"alice":    {ID: "11111111-1111-4111-8111-111111111111", Username: "alice", PasswordHash: &hash},
		"disabled": {ID: "22222222-2222-4222-8222-222222222222", Username: "disabled", PasswordHash: &hash, Disabled: true},
	}}
	authCfg := config.AuthConfig{
		SessionCookieSecure: true, SessionLifetime: time.Hour, SessionIdleTimeout: 15 * time.Minute,
		LoginRateRefillInterval: time.Minute, LoginRateBurst: 20, LoginRateMaxKeys: 100,
		LoginMaxConcurrent: 4,
	}
	if mutate != nil {
		mutate(&authCfg)
	}
	if sessionStore == nil {
		sessionStore = &controllableSessionStore{base: memstore.NewWithCleanupInterval(0)}
	}
	sessions := newSessionManager(authCfg, sessionStore)
	audit := &recordingAudit{}
	metrics := &countingMetrics{}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	logs := &strings.Builder{}
	if identity == nil {
		identity = core.NewLocalIdentityProvider(store)
	}
	authorization := newAuthAuthorization()
	mediaServers := newFakeMediaServerService()
	invites := newFakeInviteService(clock.Now())
	playback := &fakePlaybackReader{}
	srv := New(config.HTTPConfig{
		Addr: ":0", ReadHeaderTimeout: time.Second, WriteTimeout: time.Second, MaxBodyBytes: 8192,
	}, Deps{
		Logger: slog.New(slog.NewJSONHandler(logs, nil)), Metrics: metrics,
		Readiness: telemetry.NewReadiness(true), Pinger: &fakePinger{},
		Web: web, Identity: identity, Accounts: store,
		Authorizer: authorization, Roles: authorization,
		MediaServerReader: mediaServers, MediaServerManager: mediaServers,
		InviteReader:   invites,
		InviteManager:  invites,
		PlaybackReader: playback,
		Sessions:       sessions, Audit: audit, Clock: clock, Auth: authCfg,
		AuditCorrelationKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	return authHarness{
		server: srv, h: srv.Handler(), store: store, sessions: sessions, audit: audit,
		metrics: metrics, clock: clock, logs: logs, sessionStore: sessionStore,
		authorization: authorization,
		mediaServers:  mediaServers,
		invites:       invites,
		playback:      playback,
	}
}

func newAuthAuthorization() *authAuthorization {
	allPermissions := make([]core.Permission, 0, len(core.PermissionCatalog()))
	for _, definition := range core.PermissionCatalog() {
		allPermissions = append(allPermissions, definition.ID)
	}
	return &authAuthorization{
		permissions: map[string][]core.Permission{"11111111-1111-4111-8111-111111111111": allPermissions},
		roleNames:   map[string][]string{"11111111-1111-4111-8111-111111111111": {"owner"}},
		roles: []core.Role{{
			ID: "00000000-0000-4000-8000-000000000001", Name: "owner", Description: "Full access.",
			BuiltIn: true, CreatedAt: time.Unix(0, 0).UTC(), Permissions: allPermissions,
		}},
	}
}

func newSessionManager(cfg config.AuthConfig, store scs.Store) *scs.SessionManager {
	manager := scs.New()
	manager.Store = store
	manager.Lifetime = cfg.SessionLifetime
	manager.IdleTimeout = cfg.SessionIdleTimeout
	manager.Cookie.Name = sessionCookieName
	manager.Cookie.Path = "/"
	manager.Cookie.HttpOnly = true
	manager.Cookie.SameSite = http.SameSiteLaxMode
	manager.Cookie.Secure = cfg.SessionCookieSecure
	manager.HashTokenInStore = true
	return manager
}

func (h authHarness) request(t *testing.T, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	contentType := ""
	if method == http.MethodPost && path == "/api/v1/auth/login" {
		contentType = "application/json"
	}
	return h.requestWithContentType(t, method, path, body, cookie, contentType)
}

func (h authHarness) requestWithContentType(
	t *testing.T,
	method, path, body string,
	cookie *http.Cookie,
	contentType string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "https://bloom.test"+path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:4321"
	req.Header.Set("X-Request-ID", "request-1")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return rec
}

func (h authHarness) login(t *testing.T, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"username":"` + username + `","password":"` + password + `"}`
	return h.request(t, http.MethodPost, "/api/v1/auth/login", body, nil)
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			return cookie
		}
	}
	t.Fatal("response has no session cookie")
	return nil
}

func TestLoginSuccessCookieAuditAndMetric(t *testing.T) {
	h := newAuthHarness(t, nil)
	rec := h.login(t, "alice", "secret-password")
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var body currentAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Account.ID != "11111111-1111-4111-8111-111111111111" || body.Account.Username != "alice" {
		t.Errorf("response = %+v", body)
	}
	if !slices.Equal(body.Roles, []string{"owner"}) || !slices.IsSorted(body.Permissions) {
		t.Errorf("authorization = roles %v permissions %v", body.Roles, body.Permissions)
	}
	cookie := sessionCookie(t, rec)
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Errorf("cookie flags = %+v", cookie)
	}
	event := h.audit.last(t)
	if event.Actor != "11111111-1111-4111-8111-111111111111" || event.Resource != "account:11111111-1111-4111-8111-111111111111" || event.Reason != "authenticated" || event.Source != "192.0.2.10" || event.RequestID != "request-1" || event.Provider != telemetry.AuditProviderLocal {
		t.Errorf("audit event = %+v", event)
	}
	if strings.Contains(rec.Body.String(), "secret-password") {
		t.Fatal("response leaked password")
	}
	if strings.Contains(h.logs.String(), "secret-password") {
		t.Fatal("access log leaked password")
	}
	if got := h.metrics.loginOutcome(); got != "success" {
		t.Errorf("metric outcome = %q, want success", got)
	}
}

func TestLoginReturnsBootstrapAdminAuthorization(t *testing.T) {
	h := newAuthHarness(t, nil)
	response := decodeCurrentAccount(t, h.login(t, "alice", "secret-password"))
	wantPermissions := make([]core.Permission, 0, len(core.PermissionCatalog()))
	for _, definition := range core.PermissionCatalog() {
		wantPermissions = append(wantPermissions, definition.ID)
	}
	slices.Sort(wantPermissions)
	if !slices.Equal(response.Roles, []string{"owner"}) || !slices.Equal(response.Permissions, wantPermissions) {
		t.Fatalf("bootstrap admin authorization = roles %v permissions %v", response.Roles, response.Permissions)
	}
}

func TestLoginReturnsEmptyAuthorizationForRolelessAccount(t *testing.T) {
	h := newAuthHarness(t, nil)
	addRolelessLoginAccount(h, "roleless", "33333333-3333-4333-8333-333333333333")
	response := decodeCurrentAccount(t, h.login(t, "roleless", "secret-password"))
	if response.Roles == nil || response.Permissions == nil {
		t.Fatalf("roleless authorization = roles %v permissions %v, want empty arrays", response.Roles, response.Permissions)
	}
	if len(response.Roles) != 0 || len(response.Permissions) != 0 {
		t.Fatalf("roleless authorization = roles %v permissions %v", response.Roles, response.Permissions)
	}
}

func TestLoginResponseMatchesMe(t *testing.T) {
	h := newAuthHarness(t, nil)
	login := h.login(t, "alice", "secret-password")
	cookie := sessionCookie(t, login)
	me := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if login.Code != http.StatusOK || me.Code != http.StatusOK {
		t.Fatalf("statuses = login %d, me %d", login.Code, me.Code)
	}
	if login.Body.String() != me.Body.String() {
		t.Fatalf("bodies differ: login %s me %s", login.Body.String(), me.Body.String())
	}
}

func TestLoginReadsRevokedAuthorizationBeforeCreatingSession(t *testing.T) {
	h := newAuthHarness(t, nil)
	accountID := "11111111-1111-4111-8111-111111111111"
	h.authorization.beforeSnapshot = func(ctx context.Context, snapshotID string) {
		if got := h.sessions.GetString(ctx, sessionAccountIDKey); got != "" || snapshotID != accountID {
			t.Errorf("session account = %q, snapshot account = %q, want empty and %q", got, snapshotID, accountID)
		}
		h.authorization.mu.Lock()
		defer h.authorization.mu.Unlock()
		h.authorization.roleNames[accountID] = nil
		h.authorization.permissions[accountID] = nil
	}
	response := decodeCurrentAccount(t, h.login(t, "alice", "secret-password"))
	if len(response.Roles) != 0 || len(response.Permissions) != 0 {
		t.Fatalf("revoked authorization = roles %v permissions %v", response.Roles, response.Permissions)
	}
}

func TestLoginAuthorizationFailureDestroysInboundSession(t *testing.T) {
	h := newAuthHarness(t, nil)
	oldCookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.audit.reset()
	h.metrics.resetLoginOutcomes()
	h.authorization.snapshotErr = errors.New("authorization unavailable")
	recorder := h.request(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"alice","password":"secret-password"}`, oldCookie)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	if expired := sessionCookie(t, recorder); expired.MaxAge >= 0 {
		t.Fatalf("authorization failure cookie MaxAge = %d, want deletion", expired.MaxAge)
	}
	assertAuthorizationFailureTelemetry(t, h)
	if got := h.request(t, http.MethodGet, "/api/v1/auth/me", "", oldCookie).Code; got != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, want 401", got)
	}
}

func TestLoginAuthorizationFailureExpiresUnresolvedInboundSession(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.authorization.snapshotErr = errors.New("authorization unavailable")
	unresolved := &http.Cookie{Name: sessionCookieName, Value: "unresolved-token"}
	recorder := h.request(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"alice","password":"secret-password"}`, unresolved)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	if expired := sessionCookie(t, recorder); expired.MaxAge >= 0 {
		t.Fatalf("authorization failure cookie MaxAge = %d, want deletion", expired.MaxAge)
	}
	assertAuthorizationFailureTelemetry(t, h)
}

func assertAuthorizationFailureTelemetry(t *testing.T, h authHarness) {
	t.Helper()
	if outcomes := h.metrics.loginOutcomes(); !slices.Equal(outcomes, []string{"internal_error"}) {
		t.Fatalf("login metrics = %v, want [internal_error]", outcomes)
	}
	events := h.audit.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %+v, want one", events)
	}
	event := events[0]
	if event.Actor != "anonymous" || event.SubjectID != h.server.usernameSubjectID("alice") ||
		event.Action != "auth.login" || event.Resource != auditResourceAuthLogin ||
		event.Result != telemetry.AuditFailure || event.Reason != "internal_error" {
		t.Fatalf("authorization failure audit = %+v", event)
	}
}

func decodeCurrentAccount(t *testing.T, recorder *httptest.ResponseRecorder) currentAccountResponse {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var response currentAccountResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode current account: %v", err)
	}
	return response
}

func addRolelessLoginAccount(h authHarness, username, accountID string) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	hash := h.store.accounts["alice"].PasswordHash
	h.store.accounts[username] = core.Account{ID: accountID, Username: username, PasswordHash: hash}
}

func TestLoginCanonicalizesUsernameAtEveryIdentityBoundary(t *testing.T) {
	h := newAuthHarness(t, func(cfg *config.AuthConfig) { cfg.LoginRateBurst = 1 })
	rec := h.login(t, "ＡLICE", "wrong")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("fullwidth login = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	firstSubject := h.audit.last(t).SubjectID
	rec = h.login(t, "alice", "wrong")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("canonical rate-limit peer = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if secondSubject := h.audit.last(t).SubjectID; secondSubject != firstSubject {
		t.Fatalf("audit subjects differ: %q != %q", firstSubject, secondSubject)
	}
}

func TestLoginAcceptsCanonicalEquivalentUsername(t *testing.T) {
	h := newAuthHarness(t, nil)
	rec := h.login(t, "ＡLICE", "secret-password")
	if rec.Code != http.StatusOK {
		t.Fatalf("canonical-equivalent login = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginRequiresJSONContentType(t *testing.T) {
	tests := []struct {
		name, contentType string
		want              int
	}{
		{name: "missing", want: http.StatusUnsupportedMediaType},
		{name: "text", contentType: "text/plain", want: http.StatusUnsupportedMediaType},
		{name: "malformed", contentType: "application/json; charset", want: http.StatusUnsupportedMediaType},
		{name: "parameters", contentType: "application/json; charset=utf-8", want: http.StatusOK},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			rec := h.requestWithContentType(t, http.MethodPost, "/api/v1/auth/login",
				`{"username":"alice","password":"secret-password"}`, nil, testCase.contentType)
			if rec.Code != testCase.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, testCase.want, rec.Body.String())
			}
			if testCase.want == http.StatusUnsupportedMediaType {
				envelope := decodeEnvelope(t, rec)
				if envelope.Code != codeUnsupportedMediaType {
					t.Fatalf("error code = %q, want %q", envelope.Code, codeUnsupportedMediaType)
				}
			}
		})
	}
}

func TestLoginRenewsExistingSessionToken(t *testing.T) {
	h := newAuthHarness(t, nil)
	first := sessionCookie(t, h.login(t, "alice", "secret-password"))
	body := `{"username":"alice","password":"secret-password"}`
	rec := h.request(t, http.MethodPost, "/api/v1/auth/login", body, first)
	if rec.Code != http.StatusOK {
		t.Fatalf("second login = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if renewed := sessionCookie(t, rec); renewed.Value == first.Value {
		t.Fatal("login did not renew the existing session token")
	}
}

func TestLoginCredentialFailuresAreOpaque(t *testing.T) {
	tests := []struct {
		name, username, password, reason string
	}{
		{"unknown", "nobody", "secret-password", "unknown_user"},
		{"wrong password", "alice", "wrong", "bad_password"},
		{"disabled", "disabled", "secret-password", "disabled"},
	}
	var wantBody string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			rec := h.login(t, tt.username, tt.password)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if wantBody == "" {
				wantBody = rec.Body.String()
			} else if rec.Body.String() != wantBody {
				t.Errorf("body = %q, want identical %q", rec.Body.String(), wantBody)
			}
			event := h.audit.last(t)
			if event.Reason != tt.reason || event.Actor != "anonymous" || event.SubjectID == "" ||
				event.Resource != auditResourceAuthLogin || event.Provider != telemetry.AuditProviderLocal {
				t.Errorf("audit event = %+v", event)
			}
			if event.SubjectID == tt.username || strings.Contains(event.SubjectID, tt.username) {
				t.Errorf("audit subject contains submitted username: %+v", event)
			}
			if got := h.metrics.loginOutcome(); got != tt.reason {
				t.Errorf("metric outcome = %q, want %q", got, tt.reason)
			}
		})
	}
}

func TestLoginValidationAndBodyLimit(t *testing.T) {
	tests := []struct {
		name, body string
	}{
		{"malformed", `{"username":`},
		{"missing fields", `{}`},
		{"unknown field", `{"username":"alice","password":"secret-password","extra":true}`},
		{"username too long", `{"username":"` + strings.Repeat("x", core.MaxSubmittedUsernameCharacters+1) + `","password":"secret-password"}`},
		{"password too long", `{"username":"alice","password":"` + strings.Repeat("x", core.MaxPasswordBytes+1) + `"}`},
		{"oversized", `{"username":"alice","password":"` + strings.Repeat("x", 3000) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			rec := h.request(t, http.MethodPost, "/api/v1/auth/login", tt.body, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
			}
			env := decodeEnvelope(t, rec)
			if env.Code != codeValidationFailed || len(env.Fields) == 0 {
				t.Errorf("envelope = %+v", env)
			}
		})
	}
}

func TestLoginRejectsMalformedUTF8(t *testing.T) {
	h := newAuthHarness(t, nil)
	for _, body := range []string{
		`{"username":"` + string([]byte{0xff}) + `","password":"secret-password"}`,
		`{"username":"alice","password":"` + string([]byte{0xff}) + `"}`,
	} {
		if rec := h.request(t, http.MethodPost, "/api/v1/auth/login", body, nil); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("malformed UTF-8 status = %d, want 422: %s", rec.Code, rec.Body.String())
		}
	}
}

func TestLoginUsernameLengthUsesUnicodeCodePoints(t *testing.T) {
	h := newAuthHarness(t, nil)
	atLimit := `{"username":"` + strings.Repeat("界", core.MaxUsernameCharacters) + `","password":"wrong"}`
	if rec := h.request(t, http.MethodPost, "/api/v1/auth/login", atLimit, nil); rec.Code == http.StatusUnprocessableEntity {
		t.Fatalf("username at code-point limit rejected: %s", rec.Body.String())
	}
	overLimit := `{"username":"` + strings.Repeat("界", core.MaxUsernameCharacters+1) + `","password":"wrong"}`
	if rec := h.request(t, http.MethodPost, "/api/v1/auth/login", overLimit, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("username over code-point limit = %d, want 422", rec.Code)
	}
}

func TestLoginRateLimitAndRetryAfter(t *testing.T) {
	h := newAuthHarness(t, func(cfg *config.AuthConfig) { cfg.LoginRateBurst = 1 })
	if rec := h.login(t, "alice", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("first login = %d, want 401", rec.Code)
	}
	h.clock.Advance(600 * time.Millisecond)
	rec := h.login(t, "alice", "wrong")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("limited login = %d Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if event := h.audit.last(t); event.Reason != "rate_limited" || event.Actor != "anonymous" || event.SubjectID == "" || event.Resource != auditResourceAuthLogin || event.Provider != telemetry.AuditProviderLocal {
		t.Errorf("audit event = %+v", event)
	}
	if got := h.metrics.loginOutcome(); got != "rate_limited" {
		t.Errorf("metric outcome = %q, want rate_limited", got)
	}
	h.clock.Advance(time.Minute)
	if rec := h.login(t, "alice", "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("login after refill = %d, want 401", rec.Code)
	}
}

func TestUsernameSubjectIDIsKeyedNormalizedAndTruncated(t *testing.T) {
	left := &Server{usernameAuditKey: deriveUsernameAuditKey([]byte("left-secret-key-material"))}
	right := &Server{usernameAuditKey: deriveUsernameAuditKey([]byte("right-secret-key-material"))}
	lower := left.usernameSubjectID("alice.test")
	upper := left.usernameSubjectID("ＡLICE.TEST")

	if lower != upper {
		t.Fatalf("normalized subject ids differ: %q != %q", lower, upper)
	}
	if lower == right.usernameSubjectID("alice.test") {
		t.Fatal("subject id is not keyed")
	}
	if len(lower) != len("username:")+2*usernameAuditIDBytes || strings.Contains(lower, "alice") {
		t.Fatalf("subject id = %q, want fixed-length opaque identifier", lower)
	}
	if left.usernameSubjectID("Σigma") != left.usernameSubjectID("σigma") {
		t.Fatal("Greek uppercase sigma did not share its PRECIS audit key")
	}
	if left.usernameSubjectID("Σigma") == left.usernameSubjectID("ςigma") {
		t.Fatal("Greek final sigma unexpectedly shared a PRECIS audit key")
	}
	if left.usernameSubjectID("Straße") == left.usernameSubjectID("STRASSE") {
		t.Fatal("sharp s unexpectedly shared the STRASSE PRECIS audit key")
	}
}

func TestLoginInternalFailureIsOpaqueAndAudited(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.store.fail(errors.New("database unavailable"))
	rec := h.login(t, "alice", "secret-password")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := decodeEnvelope(t, rec)
	if env.Code != codeInternal || strings.Contains(rec.Body.String(), "database") {
		t.Errorf("envelope = %+v, want opaque internal", env)
	}
	if event := h.audit.last(t); event.Reason != "internal_error" {
		t.Errorf("audit event = %+v", event)
	}
	if event := h.audit.last(t); event.Actor != "anonymous" || event.SubjectID == "" || strings.Contains(fmt.Sprint(event), "alice") {
		t.Errorf("internal failure audit identity = %+v", event)
	}
}

func TestSessionRequiredRoutes(t *testing.T) {
	h := newAuthHarness(t, nil)
	for _, path := range []string{"/api/v1/auth/me", "/api/v1/auth/logout"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "logout") {
			method = http.MethodPost
		}
		rec := h.request(t, method, path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without cookie = %d, want 401", path, rec.Code)
		}
	}

	bad := &http.Cookie{Name: sessionCookieName, Value: "not-a-session"}
	if rec := h.request(t, http.MethodGet, "/api/v1/auth/me", "", bad); rec.Code != http.StatusUnauthorized {
		t.Errorf("me with bad cookie = %d, want 401", rec.Code)
	}

	data, err := h.sessions.Codec.Encode(expiredSessionInstant(), map[string]any{sessionAccountIDKey: "11111111-1111-4111-8111-111111111111"})
	if err != nil {
		t.Fatalf("encode expired session: %v", err)
	}
	if err := h.sessions.Store.Commit(hashedSessionToken("expired"), data, expiredSessionInstant()); err != nil {
		t.Fatalf("commit expired session: %v", err)
	}
	expired := &http.Cookie{Name: sessionCookieName, Value: "expired"}
	if rec := h.request(t, http.MethodGet, "/api/v1/auth/me", "", expired); rec.Code != http.StatusUnauthorized {
		t.Errorf("me with expired cookie = %d, want 401", rec.Code)
	}
	if event := h.audit.last(t); event.Reason != "invalid_session" {
		t.Errorf("expired session audit = %+v", event)
	}
}

func hashedSessionToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func expiredSessionInstant() time.Time {
	return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
}

func futureSessionInstant() time.Time {
	return time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
}

func TestLoginCommitFailureReturnsOneEnvelopeWithoutSuccessTelemetry(t *testing.T) {
	store := &controllableSessionStore{base: memstore.NewWithCleanupInterval(0), commitErr: errors.New("commit failed")}
	h := newAuthHarnessWithSessionStore(t, nil, store)
	rec := h.login(t, "alice", "secret-password")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	if env.Code != codeInternal {
		t.Fatalf("envelope = %+v", env)
	}
	dec := json.NewDecoder(strings.NewReader(rec.Body.String()))
	var first, second any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("first JSON: %v", err)
	}
	if err := dec.Decode(&second); !errors.Is(err, io.EOF) {
		t.Fatalf("response contains more than one JSON value: %v", err)
	}
	if got := h.metrics.loginOutcome(); got == "success" {
		t.Fatal("commit failure recorded login success")
	}
	if len(h.audit.events) != 1 || h.audit.events[0].Result != telemetry.AuditFailure {
		t.Fatalf("audit events = %+v, want one failure", h.audit.events)
	}
}

func TestInvalidAndRevokedSessionsAreAuditedAndCookiesExpire(t *testing.T) {
	h := newAuthHarness(t, nil)
	missing := h.request(t, http.MethodGet, "/api/v1/auth/me", "", nil)
	if missing.Code != http.StatusUnauthorized || h.audit.last(t).Reason != "missing_session" {
		t.Fatalf("missing session = %d audit %+v", missing.Code, h.audit.last(t))
	}

	invalid := h.request(t, http.MethodGet, "/api/v1/auth/me", "", &http.Cookie{Name: sessionCookieName, Value: "unknown"})
	if invalid.Code != http.StatusUnauthorized || h.audit.last(t).Reason != "invalid_session" {
		t.Fatalf("invalid session = %d audit %+v", invalid.Code, h.audit.last(t))
	}
	if cookie := sessionCookie(t, invalid); cookie.MaxAge >= 0 {
		t.Fatalf("invalid cookie MaxAge = %d, want deletion", cookie.MaxAge)
	}

	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.store.disable("alice")
	disabled := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if disabled.Code != http.StatusUnauthorized || h.audit.last(t).Reason != "account_disabled" {
		t.Fatalf("disabled session = %d audit %+v", disabled.Code, h.audit.last(t))
	}
	if cleared := sessionCookie(t, disabled); cleared.MaxAge >= 0 {
		t.Fatalf("disabled cookie MaxAge = %d", cleared.MaxAge)
	}
}

func TestLoginRenewalDeletesOldStoredToken(t *testing.T) {
	h := newAuthHarness(t, nil)
	first := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.sessionStore.mu.Lock()
	h.sessionStore.deleted = nil
	h.sessionStore.mu.Unlock()
	rec := h.request(t, http.MethodPost, "/api/v1/auth/login", `{"username":"alice","password":"secret-password"}`, first)
	if rec.Code != http.StatusOK {
		t.Fatalf("renew login = %d: %s", rec.Code, rec.Body.String())
	}
	h.sessionStore.mu.Lock()
	defer h.sessionStore.mu.Unlock()
	if len(h.sessionStore.deleted) != 1 || h.sessionStore.deleted[0] != hashedSessionToken(first.Value) {
		t.Fatalf("deleted tokens = %v", h.sessionStore.deleted)
	}
}

func TestLoginRevocationFailureDoesNotRestoreOldSession(t *testing.T) {
	h := newAuthHarness(t, nil)
	oldCookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.sessionStore.mu.Lock()
	h.sessionStore.deleteAfterErr = errors.New("delete result unavailable")
	h.sessionStore.mu.Unlock()

	recorder := h.request(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"alice","password":"secret-password"}`, oldCookie)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("revocation failure = %d, want 500", recorder.Code)
	}
	if cleared := sessionCookie(t, recorder); cleared.MaxAge >= 0 {
		t.Fatalf("revocation failure cookie MaxAge = %d, want deletion", cleared.MaxAge)
	}
	if got := h.request(t, http.MethodGet, "/api/v1/auth/me", "", oldCookie).Code; got != http.StatusUnauthorized {
		t.Fatalf("old session status = %d, want 401", got)
	}
}

func TestSessionStoreFailuresReturnOneOpaqueEnvelope(t *testing.T) {
	store := &controllableSessionStore{base: memstore.NewWithCleanupInterval(0)}
	h := newAuthHarnessWithSessionStore(t, nil, store)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	store.mu.Lock()
	store.findErr = fmt.Errorf("database unavailable: %w", core.ErrSessionStore)
	store.mu.Unlock()
	rec := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("find failure = %d %s", rec.Code, rec.Body.String())
	}
	if env := decodeEnvelope(t, rec); env.Code != codeInternal || strings.Contains(rec.Body.String(), "database") {
		t.Fatalf("find failure envelope = %+v", env)
	}

	store.mu.Lock()
	store.findErr = nil
	store.deleteErr = errors.New("delete failed")
	store.mu.Unlock()
	h.store.disable("alice")
	rec = h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if rec.Code != http.StatusInternalServerError || h.audit.last(t).Reason != "account_disabled" {
		t.Fatalf("delete failure = %d audit %+v", rec.Code, h.audit.last(t))
	}
}

func TestMalformedStoredSessionIsRejectedAndCleared(t *testing.T) {
	h := newAuthHarness(t, nil)
	token := "malformed-secret-token"
	payload := []byte("secret-malformed-payload")
	if err := h.sessions.Store.Commit(hashedSessionToken(token), payload, futureSessionInstant()); err != nil {
		t.Fatalf("Commit malformed: %v", err)
	}
	rec := h.request(t, http.MethodGet, "/api/v1/auth/me", "", &http.Cookie{Name: sessionCookieName, Value: token})
	if rec.Code != http.StatusUnauthorized || h.audit.last(t).Reason != "malformed_session" {
		t.Fatalf("malformed session = %d audit %+v", rec.Code, h.audit.last(t))
	}
	if cookie := sessionCookie(t, rec); cookie.MaxAge >= 0 {
		t.Fatalf("malformed cookie MaxAge = %d", cookie.MaxAge)
	}
	observed := h.logs.String() + fmt.Sprint(h.audit.last(t)) + rec.Body.String()
	if strings.Contains(observed, token) || strings.Contains(observed, string(payload)) {
		t.Fatalf("malformed-session signal leaked token or payload: %s", observed)
	}
}

func TestMeLogoutAndDisabledSession(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))

	rec := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"alice"`) {
		t.Fatalf("me = %d %s", rec.Code, rec.Body.String())
	}

	rec = h.request(t, http.MethodPost, "/api/v1/auth/logout", "", cookie)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("logout = %d %q, want 204 empty", rec.Code, rec.Body.String())
	}
	if event := h.audit.last(t); event.Action != "auth.logout" || event.Actor != "11111111-1111-4111-8111-111111111111" || event.Resource != "account:11111111-1111-4111-8111-111111111111" {
		t.Errorf("logout audit = %+v", event)
	}
	if rec := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("me after logout = %d, want 401", rec.Code)
	}

	cookie = sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.store.disable("alice")
	if rec := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie); rec.Code != http.StatusUnauthorized {
		t.Errorf("me for disabled account = %d, want 401", rec.Code)
	}
}

func TestDeletedAccountInvalidatesSession(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.store.delete("alice")
	rec := h.request(t, http.MethodGet, "/api/v1/auth/me", "", cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("me for deleted account = %d, want 401", rec.Code)
	}
	if event := h.audit.last(t); event.Reason != "account_deleted" || event.Actor != "11111111-1111-4111-8111-111111111111" || event.Resource != auditResourceAuthMe {
		t.Errorf("deleted account audit = %+v", event)
	}
	if cleared := sessionCookie(t, rec); cleared.MaxAge >= 0 {
		t.Errorf("deleted account cookie MaxAge = %d", cleared.MaxAge)
	}
}

func TestCrossOriginProtection(t *testing.T) {
	h := newAuthHarness(t, nil)
	body := `{"username":"alice","password":"secret-password"}`

	for _, headers := range []map[string]string{
		{"Origin": "https://evil.example"},
		{"Sec-Fetch-Site": "cross-site"},
	} {
		req := httptest.NewRequest(http.MethodPost, "https://bloom.test/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		rec := httptest.NewRecorder()
		h.h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("cross-origin POST with %v = %d, want 403", headers, rec.Code)
		}
	}
	if h.metrics.csrf != 2 {
		t.Fatalf("CSRF rejection metric = %d, want 2", h.metrics.csrf)
	}
	if event := h.audit.last(t); event.Action != "auth.csrf" || event.Reason != "cross_origin" || event.Resource != auditResourceAuthLogin {
		t.Fatalf("CSRF audit event = %+v", event)
	}

	req := httptest.NewRequest(http.MethodPost, "https://bloom.test/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://bloom.test")
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin POST = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

func TestCrossOriginProtectionMapsExactKnownRoutes(t *testing.T) {
	tests := []struct{ method, path, resource string }{
		{path: "/api/v1/auth/login", resource: auditResourceAuthLogin},
		{path: "/api/v1/auth/logout", resource: auditResourceAuthLogout},
		{path: "/api/v1/auth/me", resource: auditResourceAuthMe},
		{path: "/api/v1/auth/oidc/start", resource: auditResourceAuthOIDCStart},
		{path: "/api/v1/media-servers", resource: auditResourceMediaServers},
		{path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333/probe", resource: auditResourceMediaServers},
		{method: http.MethodDelete, path: "/api/v1/media-servers/33333333-3333-4333-8333-333333333333", resource: auditResourceMediaServers},
		{path: "/api/v1/invites", resource: auditResourceInvites},
		{path: "/api/v1/invite/ABCDEFGHIJKLMNOPQRSTUVWXYZ/accept", resource: auditResourceInvitePublic},
		{method: http.MethodPut, path: "/api/v1/metadata/providers/tmdb/key", resource: auditResourceMetadataSettings},
		{path: "/api/v1/request-profiles", resource: auditResourceRequestProfiles},
		{method: http.MethodPut, path: "/api/v1/request-profiles/33333333-3333-4333-8333-333333333333", resource: auditResourceRequestProfiles},
		{path: "/api/v1/requests", resource: auditResourceRequests},
		{path: "/api/v1/requests/33333333-3333-4333-8333-333333333333/approve", resource: auditResourceRequests},
		{path: "/api/v1/requests/33333333-3333-4333-8333-333333333333/decline", resource: auditResourceRequests},
		{method: http.MethodPut, path: "/api/v1/roles/33333333-3333-4333-8333-333333333333/request-quota", resource: auditResourceRequestQuotas},
		{method: http.MethodPut, path: "/api/v1/accounts/33333333-3333-4333-8333-333333333333/request-quota", resource: auditResourceRequestQuotas},
	}
	for _, testCase := range tests {
		t.Run(testCase.path, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			method := testCase.method
			if method == "" {
				method = http.MethodPost
			}
			req := httptest.NewRequest(method, "https://bloom.test"+testCase.path, nil)
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			rec := httptest.NewRecorder()
			h.h.ServeHTTP(rec, req)
			if event := h.audit.last(t); rec.Code != http.StatusForbidden || event.Resource != testCase.resource {
				t.Fatalf("CSRF response = %d, audit = %+v", rec.Code, event)
			}
		})
	}
}

func TestCrossOriginArbitraryPathDoesNotEnterAuditRecord(t *testing.T) {
	h := newAuthHarness(t, nil)
	sensitivePath := "/private/" + strings.Repeat("customer-secret-", 256)
	req := httptest.NewRequest(http.MethodPost, "https://bloom.test"+sensitivePath, nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)

	event := h.audit.last(t)
	if rec.Code != http.StatusForbidden || event.Resource != auditResourceRouteUnmatched {
		t.Fatalf("CSRF response = %d, audit = %+v", rec.Code, event)
	}
	if strings.Contains(fmt.Sprint(event), sensitivePath) || strings.Contains(fmt.Sprint(event), "customer-secret") {
		t.Fatalf("audit event contains raw request path: %+v", event)
	}
}

func TestAuditSinkFailureIsLoggedCountedAndDoesNotBlockLogin(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.server.audit = telemetry.NewAuditLogger(failingAuditWriter{}, h.clock)
	rec := h.login(t, "alice", "secret-password")

	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := h.metrics.auditFailureCount(); got != 1 {
		t.Fatalf("audit failure metric = %d, want 1", got)
	}
	if got := strings.Count(h.logs.String(), "write audit event"); got != 1 {
		t.Fatalf("audit operational errors = %d, want 1: %s", got, h.logs.String())
	}
}

type failingAuditWriter struct{}

func (failingAuditWriter) Write([]byte) (int, error) {
	return 0, errors.New("audit writer failed")
}

func TestClientIPTrustsForwardingOnlyFromConfiguredProxy(t *testing.T) {
	t.Parallel()

	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.0.2.0/24")}
	tests := []struct {
		name, remote, want string
		forwarded          []string
	}{
		{name: "off by default", remote: "10.0.0.2:443", forwarded: []string{"198.51.100.9"}, want: "10.0.0.2"},
		{name: "untrusted peer", remote: "203.0.113.4:443", forwarded: []string{"198.51.100.9"}, want: "203.0.113.4"},
		{name: "rightmost untrusted", remote: "10.0.0.2:443", forwarded: []string{"198.51.100.9, 192.0.2.8"}, want: "198.51.100.9"},
		{name: "duplicate fields preserve order", remote: "10.0.0.2:443", forwarded: []string{"198.51.100.9", "192.0.2.8"}, want: "198.51.100.9"},
		{name: "malformed trusted chain fails closed", remote: "10.0.0.2:443", forwarded: []string{"198.51.100.9", "bad, 192.0.2.8"}, want: "10.0.0.2"},
		{name: "stop before malformed attacker value", remote: "10.0.0.2:443", forwarded: []string{"bad", "198.51.100.9"}, want: "198.51.100.9"},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "https://bloom.test/", nil)
		req.RemoteAddr = tt.remote
		for _, value := range tt.forwarded {
			req.Header.Add("X-Forwarded-For", value)
		}
		prefixes := trusted
		if tt.name == "off by default" {
			prefixes = nil
		}
		if got := clientIP(req, prefixes); got != tt.want {
			t.Errorf("%s: clientIP = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func (m *countingMetrics) loginOutcome() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.logins) == 0 {
		return ""
	}
	return m.logins[len(m.logins)-1]
}

func (m *countingMetrics) loginOutcomes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.logins)
}

func (m *countingMetrics) resetLoginOutcomes() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logins = nil
}

func (m *countingMetrics) loginProvider() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.loginProviders) == 0 {
		return ""
	}
	return m.loginProviders[len(m.loginProviders)-1]
}
