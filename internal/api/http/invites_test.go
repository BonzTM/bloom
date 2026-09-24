package http

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
	inviteapp "github.com/BonzTM/bloom/internal/invite"
	"github.com/BonzTM/bloom/internal/telemetry"
)

const testInviteCode = "AAAAAAAAAAAAAAAAAAAAAAAAAA"

type fakeInviteService struct {
	mu        sync.Mutex
	invite    core.Invite
	err       error
	created   int
	listed    int
	got       int
	revoked   int
	previewed int
	accepted  int
	deadline  bool
	accountID string
	failure   core.InviteProvisioningFailure
	dismissed string
}

type serviceBackedInviteStore struct {
	invite     core.Invite
	lookupHash *[sha256.Size]byte
	lookups    int
	redeems    int
}

func (s *serviceBackedInviteStore) CreateInvite(
	_ context.Context, value core.Invite, _ [sha256.Size]byte,
) error {
	s.invite = value
	return nil
}

func (s *serviceBackedInviteStore) RevokeInvite(context.Context, string, time.Time) (core.Invite, error) {
	return s.invite, nil
}

func (s *serviceBackedInviteStore) RedeemInvite(
	ctx context.Context, _ [sha256.Size]byte, _ core.Clock, redeem core.InviteRedeemFunc,
) (bool, error) {
	s.redeems++
	_, err := redeem(ctx, s.invite)
	return false, err
}

func (*serviceBackedInviteStore) RecordInviteProvisioningFailure(
	context.Context, core.InviteProvisioningFailure,
) error {
	return nil
}

func (s *serviceBackedInviteStore) GetInvite(context.Context, string) (core.Invite, error) {
	return s.invite, nil
}

func (s *serviceBackedInviteStore) GetInviteByCodeHash(
	_ context.Context, hash [sha256.Size]byte,
) (core.InviteCodeLookup, error) {
	s.lookups++
	stored := hash
	if s.lookupHash != nil {
		stored = *s.lookupHash
	}
	return core.InviteCodeLookup{Invite: s.invite, CodeHash: stored}, nil
}

func (*serviceBackedInviteStore) ClaimInviteProvisioningFailure(context.Context, core.InviteProvisioningLease, time.Time) (core.InviteProvisioningFailure, error) {
	return core.InviteProvisioningFailure{}, core.ErrNotFound
}

func (*serviceBackedInviteStore) CompleteInviteProvisioningCleanup(context.Context, string, string) error {
	return nil
}

func (*serviceBackedInviteStore) CompleteInviteProvisioningPolicy(context.Context, core.InviteProvisioningFailure, core.InviteRedemption, time.Time) error {
	return nil
}

func (*serviceBackedInviteStore) RescheduleInviteProvisioningFailure(context.Context, string, string, string, time.Time, time.Time, bool) error {
	return nil
}

func (*serviceBackedInviteStore) ListInviteProvisioningFailures(context.Context, *core.InviteProvisioningFailureCursor, int) ([]core.InviteProvisioningFailure, error) {
	return nil, nil
}

func (*serviceBackedInviteStore) DismissInviteProvisioningFailure(context.Context, string, time.Time) error {
	return nil
}

func (*serviceBackedInviteStore) InviteProvisioningFailureDepth(context.Context) (int64, error) {
	return 0, nil
}

func (s *serviceBackedInviteStore) ListInvites(context.Context, *core.InviteCursor, int) ([]core.Invite, error) {
	return []core.Invite{s.invite}, nil
}

type serviceBackedInviteServers struct{}

func (serviceBackedInviteServers) Get(context.Context, string) (core.MediaServerConnection, error) {
	return core.MediaServerConnection{Server: core.MediaServer{ID: "33333333-3333-4333-8333-333333333333", Name: "Home"}}, nil
}

func (serviceBackedInviteServers) Libraries(context.Context, string) ([]core.Library, error) {
	return []core.Library{}, nil
}

func (serviceBackedInviteServers) ListInviteServers(context.Context, string, int) ([]core.InviteServer, error) {
	return []core.InviteServer{{ID: "33333333-3333-4333-8333-333333333333", Name: "Home"}}, nil
}

type unusedInviteProvisioners struct{}

func (unusedInviteProvisioners) AcquireUserProvisioner(
	context.Context, string,
) (core.MediaUserProvisioner, func(), error) {
	return nil, func() {}, nil
}

type panicInviteProvisioner struct {
	code, password   string
	created, deleted bool
}

func (p *panicInviteProvisioner) CreateUser(_ context.Context, _, password string) (core.MediaUser, error) {
	p.created = true
	p.password = password
	return core.MediaUser{ID: "55555555-5555-4555-8555-555555555555", Name: "new-user", Created: true}, nil
}

func (p *panicInviteProvisioner) SetLibraryAccess(context.Context, string, []string, bool) error {
	panic("provisioning panic " + p.password + " " + p.code)
}

func (p *panicInviteProvisioner) DeleteUser(context.Context, string) error {
	p.deleted = true
	return nil
}

type panicInviteAcquirer struct{ provisioner *panicInviteProvisioner }

func (a panicInviteAcquirer) AcquireUserProvisioner(context.Context, string) (core.MediaUserProvisioner, func(), error) {
	return a.provisioner, func() {}, nil
}

func newFakeInviteService(now time.Time) *fakeInviteService {
	uses := 1
	return &fakeInviteService{invite: core.Invite{
		ID:            "44444444-4444-4444-8444-444444444444",
		MediaServerID: "33333333-3333-4333-8333-333333333333",
		CreatedBy:     "11111111-1111-4111-8111-111111111111", Label: "Friends",
		MaxUses: &uses, LibraryIDs: []string{"11111111-1111-4111-8111-111111111111"},
		CreatedAt: now, UpdatedAt: now,
	}}
}

func (f *fakeInviteService) Create(_ context.Context, _ inviteapp.CreateInput) (inviteapp.Created, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	return inviteapp.Created{Invite: f.invite, Code: testInviteCode}, f.err
}

func (f *fakeInviteService) List(_ context.Context, _ *core.InviteCursor, _ int) ([]core.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listed++
	return []core.Invite{f.invite}, f.err
}

func (f *fakeInviteService) Get(_ context.Context, _ string) (core.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got++
	return f.invite, f.err
}

func (f *fakeInviteService) Revoke(_ context.Context, _ string) (core.Invite, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked++
	return f.invite, f.err
}

func (f *fakeInviteService) Preview(_ context.Context, _ string) (inviteapp.Preview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previewed++
	return inviteapp.Preview{Invite: f.invite, MediaServerName: "Home"}, f.err
}

func (f *fakeInviteService) Accept(ctx context.Context, accountID, _, username, _ string) (inviteapp.Accepted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accepted++
	f.accountID = accountID
	_, f.deadline = ctx.Deadline()
	return inviteapp.Accepted{InviteID: f.invite.ID, MediaServerName: "Home", Username: username}, f.err
}

func (f *fakeInviteService) ListServers(context.Context, string, int) ([]core.InviteServer, error) {
	return []core.InviteServer{{ID: f.invite.MediaServerID, Name: "Home"}}, f.err
}

func (f *fakeInviteService) ListProvisioningFailures(context.Context, *core.InviteProvisioningFailureCursor, int) ([]core.InviteProvisioningFailure, error) {
	if f.failure.ID == "" {
		return []core.InviteProvisioningFailure{}, f.err
	}
	return []core.InviteProvisioningFailure{f.failure}, f.err
}

func (f *fakeInviteService) DismissProvisioningFailure(_ context.Context, id string) error {
	f.dismissed = id
	return f.err
}

func TestSignedInInviteAcceptancePassesAccount(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	response := h.requestWithContentType(t, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept",
		`{"username":"new-user","password":"Th1s-is-a-unique-password!"}`, cookie, "application/json")
	if response.Code != http.StatusCreated {
		t.Fatalf("accept = %d: %s", response.Code, response.Body.String())
	}
	h.invites.mu.Lock()
	accountID := h.invites.accountID
	h.invites.mu.Unlock()
	if want := h.store.accounts["alice"].ID; accountID != want {
		t.Fatalf("accept account ID = %q, want %q", accountID, want)
	}
}

func TestInviteAcceptanceRejectsInvalidInboundSessions(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, authHarness) *http.Cookie
	}{
		{name: "expired", prepare: expiredInviteSession},
		{name: "unknown", prepare: func(*testing.T, authHarness) *http.Cookie {
			return &http.Cookie{Name: sessionCookieName, Value: "unknown-invite-session"}
		}},
		{name: "deleted account", prepare: deletedAccountInviteSession},
		{name: "disabled account", prepare: disabledAccountInviteSession},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			cookie := testCase.prepare(t, h)
			response := acceptInviteRequestWithCookie(t, h, cookie)
			h.invites.mu.Lock()
			accepted := h.invites.accepted
			h.invites.mu.Unlock()
			if response.Code != http.StatusUnauthorized || accepted != 0 {
				t.Fatalf("accept = %d, calls = %d: %s", response.Code, accepted, response.Body.String())
			}
			if cleared := sessionCookie(t, response); cleared.MaxAge >= 0 {
				t.Fatalf("cleared cookie MaxAge = %d", cleared.MaxAge)
			}
		})
	}
}

func TestInviteAcceptanceRejectsUnparseableSessionCookieHeaders(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{name: "unterminated quoted value", header: `bloom_session="unterminated`},
		{name: "cookie count limit", header: strings.Repeat("decoy=1;", 3000) + "bloom_session=unknown"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			response := acceptInviteRequestWithRawCookie(t, h, testCase.header)
			h.invites.mu.Lock()
			accepted := h.invites.accepted
			h.invites.mu.Unlock()
			if response.Code != http.StatusUnauthorized || accepted != 0 {
				t.Fatalf("accept = %d, calls = %d: %s", response.Code, accepted, response.Body.String())
			}
			if cleared := sessionCookie(t, response); cleared.MaxAge >= 0 {
				t.Fatalf("cleared cookie MaxAge = %d", cleared.MaxAge)
			}
		})
	}
}

func expiredInviteSession(t *testing.T, h authHarness) *http.Cookie {
	t.Helper()
	data, err := h.sessions.Codec.Encode(expiredSessionInstant(), map[string]any{
		sessionAccountIDKey: h.store.accounts["alice"].ID,
	})
	if err != nil {
		t.Fatalf("encode expired session: %v", err)
	}
	if err := h.sessions.Store.Commit(hashedSessionToken("expired-invite"), data, expiredSessionInstant()); err != nil {
		t.Fatalf("commit expired session: %v", err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: "expired-invite"}
}

func deletedAccountInviteSession(t *testing.T, h authHarness) *http.Cookie {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.store.delete("alice")
	return cookie
}

func disabledAccountInviteSession(t *testing.T, h authHarness) *http.Cookie {
	t.Helper()
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.store.disableAlice()
	return cookie
}

func acceptInviteRequestWithCookie(
	t *testing.T, h authHarness, cookie *http.Cookie,
) *httptest.ResponseRecorder {
	t.Helper()
	return h.requestWithContentType(t, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept",
		`{"username":"new-user","password":"Th1s-is-a-unique-password!"}`, cookie, "application/json")
}

func acceptInviteRequestWithRawCookie(t *testing.T, h authHarness, cookieHeader string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "https://bloom.test/api/v1/invite/"+testInviteCode+"/accept",
		strings.NewReader(`{"username":"new-user","password":"Th1s-is-a-unique-password!"}`))
	request.RemoteAddr = "192.0.2.10:4321"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", cookieHeader)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	h.h.ServeHTTP(response, request)
	return response
}

func TestInviteAuditMetricsAndAcceptanceDeadline(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	create := h.requestWithContentType(t, http.MethodPost, "/api/v1/invites",
		`{"media_server_id":"33333333-3333-4333-8333-333333333333","label":"Friends"}`,
		cookie, "application/json")
	if create.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", create.Code, create.Body.String())
	}
	if event := h.audit.last(t); event.Action != "invite.create" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("create audit = %+v", event)
	}
	revoke := h.request(t, http.MethodDelete, "/api/v1/invites/44444444-4444-4444-8444-444444444444", "", cookie)
	if revoke.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", revoke.Code, revoke.Body.String())
	}
	if event := h.audit.last(t); event.Action != "invite.revoke" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("revoke audit = %+v", event)
	}
	if accept := acceptInviteRequestForTest(t, h); accept.Code != http.StatusCreated {
		t.Fatalf("accept = %d: %s", accept.Code, accept.Body.String())
	}
	h.invites.mu.Lock()
	deadline := h.invites.deadline
	h.invites.mu.Unlock()
	if !deadline {
		t.Fatal("acceptance service did not receive a total deadline")
	}
	h.metrics.mu.Lock()
	creates, accepts := slices.Clone(h.metrics.inviteCreates), slices.Clone(h.metrics.inviteAccepts)
	h.metrics.mu.Unlock()
	if !slices.Contains(creates, "success") || !slices.Contains(accepts, "accepted") {
		t.Fatalf("invite metric outcomes create=%v accept=%v", creates, accepts)
	}
}

func TestInviteEndpoints(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	create := h.requestWithContentType(t, http.MethodPost, "/api/v1/invites",
		`{"media_server_id":"33333333-3333-4333-8333-333333333333","label":"Friends","max_uses":1}`, cookie, "application/json")
	if create.Code != http.StatusCreated || !strings.Contains(create.Body.String(), `"accept_path":"/invite/`+testInviteCode+`"`) {
		t.Fatalf("create = %d %s", create.Code, create.Body.String())
	}
	paths := []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/api/v1/invites", 200},
		{http.MethodGet, "/api/v1/invites/44444444-4444-4444-8444-444444444444", 200},
		{http.MethodDelete, "/api/v1/invites/44444444-4444-4444-8444-444444444444", 204},
		{http.MethodGet, "/api/v1/invite/" + testInviteCode, 200},
	}
	for _, testCase := range paths {
		var routeCookie *http.Cookie
		if strings.HasPrefix(testCase.path, "/api/v1/invites") {
			routeCookie = cookie
		}
		recorder := h.request(t, testCase.method, testCase.path, "", routeCookie)
		if recorder.Code != testCase.status {
			t.Errorf("%s %s = %d: %s", testCase.method, testCase.path, recorder.Code, recorder.Body.String())
		}
	}
	accept := h.requestWithContentType(t, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept",
		`{"username":"new-user","password":"Th1s-is-a-unique-password!"}`, nil, "application/json")
	if accept.Code != http.StatusCreated || strings.Contains(accept.Body.String(), "password") {
		t.Fatalf("accept = %d %s", accept.Code, accept.Body.String())
	}
	if event := h.audit.last(t); event.Action != "invite.accept" || event.Result != telemetry.AuditSuccess || event.Resource != "invite:44444444-4444-4444-8444-444444444444" {
		t.Fatalf("accept audit = %+v", event)
	}
}

func TestInvitePermissionAndCSRF(t *testing.T) {
	h := newAuthHarness(t, nil)
	if recorder := h.request(t, http.MethodGet, "/api/v1/invites", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing session = %d", recorder.Code)
	}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.mu.Lock()
	h.authorization.permissions["11111111-1111-4111-8111-111111111111"] = nil
	h.authorization.mu.Unlock()
	if recorder := h.request(t, http.MethodGet, "/api/v1/invites", "", cookie); recorder.Code != http.StatusForbidden {
		t.Fatalf("missing permission = %d", recorder.Code)
	}
	csrf := crossOriginRequest(h, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept",
		`{"username":"new-user","password":"Th1s-is-a-unique-password!"}`)
	if csrf.Code != http.StatusForbidden || h.audit.last(t).Resource != auditResourceInvitePublic {
		t.Fatalf("CSRF = %d, audit = %+v", csrf.Code, h.audit.last(t))
	}
}

func TestInviteServerListUsesUsersInviteWithoutAdminSettings(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	accountID := h.store.accounts["alice"].ID
	h.authorization.mu.Lock()
	h.authorization.permissions[accountID] = []core.Permission{core.PermissionUsersInvite}
	h.authorization.mu.Unlock()

	response := h.request(t, http.MethodGet, "/api/v1/invites/servers", "", cookie)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "base_url") ||
		strings.Contains(response.Body.String(), "capabilities") || strings.Contains(response.Body.String(), "credential") {
		t.Fatalf("least-privilege servers = %d: %s", response.Code, response.Body.String())
	}
	if denied := h.request(t, http.MethodGet, "/api/v1/invites/provisioning-failures", "", cookie); denied.Code != http.StatusForbidden {
		t.Fatalf("admin failure list with users.invite = %d", denied.Code)
	}
	h.authorization.mu.Lock()
	h.authorization.permissions[accountID] = nil
	h.authorization.mu.Unlock()
	if denied := h.request(t, http.MethodGet, "/api/v1/invites/servers", "", cookie); denied.Code != http.StatusForbidden {
		t.Fatalf("server list without users.invite = %d", denied.Code)
	}
}

func TestInviteProvisioningFailureAdministration(t *testing.T) {
	h := newAuthHarness(t, nil)
	now := h.clock.Now()
	h.invites.failure = core.InviteProvisioningFailure{
		ID: "55555555-5555-4555-8555-555555555555", InviteID: h.invites.invite.ID,
		MediaServerID: h.invites.invite.MediaServerID, MediaServerName: "Home", MediaUserOwned: true,
		Username: "new-user",
		Reason:   core.InviteProvisioningCleanupFailed, Attempts: 2, NextAttemptAt: now.Add(time.Minute),
		LastError: "media server reconciliation failed", CreatedAt: now, UpdatedAt: now,
	}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	list := h.request(t, http.MethodGet, "/api/v1/invites/provisioning-failures", "", cookie)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"media_user_owned":true`) ||
		strings.Contains(list.Body.String(), "media_user_id") ||
		strings.Contains(list.Body.String(), "account_id") || strings.Contains(list.Body.String(), "lease") {
		t.Fatalf("failure list = %d: %s", list.Code, list.Body.String())
	}
	path := "/api/v1/invites/provisioning-failures/" + h.invites.failure.ID
	dismiss := h.request(t, http.MethodDelete, path, "", cookie)
	if dismiss.Code != http.StatusNoContent || h.invites.dismissed != h.invites.failure.ID {
		t.Fatalf("dismiss = %d id=%q", dismiss.Code, h.invites.dismissed)
	}
	if event := h.audit.last(t); event.Action != "invite.provisioning_failure.dismiss" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("dismiss audit = %+v", event)
	}
	h.invites.err = core.ErrInviteProvisioningFailureLeased
	conflict := h.request(t, http.MethodDelete, path, "", cookie)
	if envelope := decodeEnvelope(t, conflict); conflict.Code != http.StatusConflict || envelope.Code != codeInviteFailureLeased {
		t.Fatalf("leased dismissal = %d %+v", conflict.Code, envelope)
	}
	assertHandlerContract(t, loadOpenAPI(t), conflict, authContractCase{
		path: "/api/v1/invites/provisioning-failures/{id}", method: "delete",
		status: http.StatusConflict, schema: errorSchema,
		headers: []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"},
	})
	if event := h.audit.last(t); event.Action != "invite.provisioning_failure.dismiss" || event.Result != telemetry.AuditFailure {
		t.Fatalf("leased dismissal audit = %+v", event)
	}
}

func TestInviteValidationAndOpaqueUnavailable(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	invalidCreate := h.requestWithContentType(t, http.MethodPost, "/api/v1/invites", `{}`, cookie, "application/json")
	if invalidCreate.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid create = %d", invalidCreate.Code)
	}
	h.invites.err = &inviteapp.LibrarySelectionError{}
	unknownLibrary := h.requestWithContentType(t, http.MethodPost, "/api/v1/invites",
		`{"media_server_id":"33333333-3333-4333-8333-333333333333","label":"Friends","library_ids":["unknown"]}`,
		cookie, "application/json")
	if unknownLibrary.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown library = %d: %s", unknownLibrary.Code, unknownLibrary.Body.String())
	}
	h.invites.err = nil
	invalidAccept := h.requestWithContentType(t, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept",
		`{"username":" bad ","password":"short"}`, nil, "application/json")
	if invalidAccept.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid accept = %d", invalidAccept.Code)
	}
	var body string
	for _, cause := range []error{core.ErrInviteUnavailable, errors.Join(core.ErrInviteUnavailable, errors.New("expired"))} {
		h.invites.err = cause
		recorder := h.request(t, http.MethodGet, "/api/v1/invite/"+testInviteCode, "", nil)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("unavailable = %d %s", recorder.Code, recorder.Body.String())
		}
		if body != "" && body != recorder.Body.String() {
			t.Fatalf("opaque bodies differ: %q != %q", body, recorder.Body.String())
		}
		body = recorder.Body.String()
	}
}

type unusableInviteRoute struct {
	method, path, body, contentType string
}

func TestUnusableInviteResponsesAndStoreWorkAreIdentical(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	routes := []unusableInviteRoute{
		{method: http.MethodGet, path: "/api/v1/invite/" + testInviteCode},
		{
			method: http.MethodPost, path: "/api/v1/invite/" + testInviteCode + "/accept",
			body: `{"username":"new-user","password":"Th1s-is-a-unique-password!"}`, contentType: "application/json",
		},
	}
	for _, route := range routes {
		var expectedBody string
		for name, store := range unusableInviteStores(now) {
			t.Run(route.method+"/"+name, func(t *testing.T) {
				body := unusableInviteResponse(t, store, route)
				if expectedBody == "" {
					expectedBody = body
				} else if body != expectedBody {
					t.Fatalf("body = %q, want %q", body, expectedBody)
				}
			})
		}
	}
}

func unusableInviteStores(now time.Time) map[string]*serviceBackedInviteStore {
	base := newFakeInviteService(now).invite
	unknown := sha256.Sum256([]byte("unknown invite digest"))
	expiredAt, revokedAt, one := now.Add(-time.Second), now.Add(-time.Minute), 1
	expired, exhausted, revoked := base, base, base
	expired.ExpiresAt = &expiredAt
	exhausted.MaxUses, exhausted.UseCount = &one, 1
	revoked.RevokedAt = &revokedAt
	return map[string]*serviceBackedInviteStore{
		"unknown": {invite: base, lookupHash: &unknown}, "expired": {invite: expired},
		"exhausted": {invite: exhausted}, "revoked": {invite: revoked},
	}
}

func unusableInviteResponse(
	t *testing.T, store *serviceBackedInviteStore, route unusableInviteRoute,
) string {
	t.Helper()
	h := newAuthHarness(t, nil)
	service, err := inviteapp.NewService(
		store, store, serviceBackedInviteServers{}, unusedInviteProvisioners{}, h.clock, slog.New(slog.DiscardHandler),
	)
	if err != nil {
		t.Fatal(err)
	}
	h.server.inviteReader, h.server.inviteManager = service, service
	recorder := h.requestWithContentType(t, route.method, route.path, route.body, nil, route.contentType)
	if recorder.Code != http.StatusNotFound || store.lookups != 1 || store.redeems != 0 {
		t.Fatalf("response=%d lookups=%d redeems=%d: %s", recorder.Code, store.lookups, store.redeems, recorder.Body.String())
	}
	return recorder.Body.String()
}

func TestInviteAcceptanceErrorMappingAndRateLimit(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "taken", err: &core.MediaUserNameError{}, status: http.StatusConflict},
		{name: "upstream", err: &core.MediaServerError{Kind: core.MediaServerUnavailable, Operation: "create_user"}, status: http.StatusBadGateway},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			h.invites.err = testCase.err
			recorder := acceptInviteRequestForTest(t, h)
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.status, recorder.Body.String())
			}
		})
	}
	h := newAuthHarness(t, func(cfg *config.AuthConfig) { cfg.LoginRateBurst = 1 })
	if first := h.request(t, http.MethodGet, "/api/v1/invite/"+testInviteCode, "", nil); first.Code != http.StatusOK {
		t.Fatalf("first preview = %d", first.Code)
	}
	limited := h.request(t, http.MethodGet, "/api/v1/invite/"+testInviteCode, "", nil)
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("limited preview = %d Retry-After=%q", limited.Code, limited.Header().Get("Retry-After"))
	}
}

func TestInvitePendingCleanupReturns502WithDistinctTelemetry(t *testing.T) {
	h := newAuthHarness(t, nil)
	h.invites.err = core.ErrInviteProvisioningPending
	recorder := acceptInviteRequestForTest(t, h)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502: %s", recorder.Code, recorder.Body.String())
	}
	event := h.audit.last(t)
	if event.Action != "invite.accept" || event.Result != telemetry.AuditFailure ||
		event.Reason != "provisioning_cleanup_pending" {
		t.Fatalf("audit event = %+v", event)
	}
	h.metrics.mu.Lock()
	outcomes := slices.Clone(h.metrics.inviteAccepts)
	h.metrics.mu.Unlock()
	if !slices.Contains(outcomes, "provisioning_cleanup_pending") {
		t.Fatalf("invite acceptance metrics = %v", outcomes)
	}
}

func TestInviteAcceptanceRateLimitsBeforeParsing(t *testing.T) {
	tests := []struct {
		name, contentType, body string
		want                    int
	}{
		{name: "unsupported media type", contentType: "text/plain", body: `{}`, want: http.StatusUnsupportedMediaType},
		{name: "malformed JSON", contentType: "application/json", body: `{"username":`, want: http.StatusUnprocessableEntity},
		{name: "invalid fields", contentType: "application/json", body: `{}`, want: http.StatusUnprocessableEntity},
	}
	for _, testCase := range tests {
		for _, trusted := range []bool{false, true} {
			name := testCase.name + "/untrusted proxy"
			if trusted {
				name = testCase.name + "/trusted proxy"
			}
			t.Run(name, func(t *testing.T) {
				h := inviteRateLimitHarness(t, trusted)
				first := inviteRequestWithForwardedFor(t, h, testInviteCode, testCase.body, testCase.contentType, "198.51.100.10")
				if first.Code != testCase.want {
					t.Fatalf("first status = %d, want %d: %s", first.Code, testCase.want, first.Body.String())
				}
				second := inviteRequestWithForwardedFor(t, h, testInviteCode, testCase.body, testCase.contentType, "198.51.100.11")
				if second.Code != http.StatusTooManyRequests {
					t.Fatalf("second status = %d, want 429: %s", second.Code, second.Body.String())
				}
			})
		}
	}
}

func TestInviteAcceptanceRateLimitHonorsOnlyTrustedForwardedFor(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		trusted bool
		want    int
	}{
		{name: "untrusted", want: http.StatusTooManyRequests},
		{name: "trusted", trusted: true, want: http.StatusUnsupportedMediaType},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := inviteRateLimitHarness(t, testCase.trusted)
			firstCode := "AAAAAAAAAAAAAAAAAAAAAAAAAA"
			secondCode := "BBBBBBBBBBBBBBBBBBBBBBBBBB"
			first := inviteRequestWithForwardedFor(t, h, firstCode, `{}`, "text/plain", "198.51.100.10")
			if first.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("first status = %d: %s", first.Code, first.Body.String())
			}
			second := inviteRequestWithForwardedFor(t, h, secondCode, `{}`, "text/plain", "198.51.100.11")
			if second.Code != testCase.want {
				t.Fatalf("second status = %d, want %d: %s", second.Code, testCase.want, second.Body.String())
			}
		})
	}
}

func inviteRateLimitHarness(t *testing.T, trusted bool) authHarness {
	t.Helper()
	return newAuthHarness(t, func(cfg *config.AuthConfig) {
		cfg.LoginRateBurst = 1
		if trusted {
			cfg.TrustedProxyCIDRs = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
		}
	})
}

func inviteRequestWithForwardedFor(
	t *testing.T, h authHarness, code, body, contentType, forwardedFor string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "https://bloom.test/api/v1/invite/"+code+"/accept", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:4321"
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Forwarded-For", forwardedFor)
	recorder := httptest.NewRecorder()
	h.h.ServeHTTP(recorder, req)
	return recorder
}

func acceptInviteRequestForTest(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return h.requestWithContentType(t, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept",
		`{"username":"new-user","password":"Th1s-is-a-unique-password!"}`, nil, "application/json")
}

func TestInviteOpenAPIContractWithValidFixtures(t *testing.T) {
	document := loadOpenAPI(t)
	tests := []struct {
		name, method, specPath, requestPath, body, schema string
		status                                            int
		public                                            bool
	}{
		{name: "create", method: http.MethodPost, specPath: "/api/v1/invites", requestPath: "/api/v1/invites", body: `{"media_server_id":"33333333-3333-4333-8333-333333333333","label":"Friends","max_uses":1}`, schema: "#/components/schemas/CreateInviteResponse", status: 201},
		{name: "list", method: http.MethodGet, specPath: "/api/v1/invites", requestPath: "/api/v1/invites", schema: "#/components/schemas/InvitesResponse", status: 200},
		{name: "servers", method: http.MethodGet, specPath: "/api/v1/invites/servers", requestPath: "/api/v1/invites/servers", schema: "#/components/schemas/InviteServersResponse", status: 200},
		{name: "failures", method: http.MethodGet, specPath: "/api/v1/invites/provisioning-failures", requestPath: "/api/v1/invites/provisioning-failures", schema: "#/components/schemas/InviteProvisioningFailuresResponse", status: 200},
		{name: "dismiss failure", method: http.MethodDelete, specPath: "/api/v1/invites/provisioning-failures/{id}", requestPath: "/api/v1/invites/provisioning-failures/55555555-5555-4555-8555-555555555555", status: 204},
		{name: "get", method: http.MethodGet, specPath: "/api/v1/invites/{id}", requestPath: "/api/v1/invites/44444444-4444-4444-8444-444444444444", schema: "#/components/schemas/Invite", status: 200},
		{name: "revoke", method: http.MethodDelete, specPath: "/api/v1/invites/{id}", requestPath: "/api/v1/invites/44444444-4444-4444-8444-444444444444", status: 204},
		{name: "preview", method: http.MethodGet, specPath: "/api/v1/invite/{code}", requestPath: "/api/v1/invite/" + testInviteCode, schema: "#/components/schemas/PublicInviteResponse", status: 200, public: true},
		{name: "accept", method: http.MethodPost, specPath: "/api/v1/invite/{code}/accept", requestPath: "/api/v1/invite/" + testInviteCode + "/accept", body: `{"username":"new-user","password":"Th1s-is-a-unique-password!"}`, schema: "#/components/schemas/AcceptInviteResponse", status: 201, public: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			var cookie *http.Cookie
			if !testCase.public {
				cookie = sessionCookie(t, h.login(t, "alice", "secret-password"))
			}
			contentType := ""
			if testCase.body != "" {
				contentType = "application/json"
			}
			recorder := h.requestWithContentType(t, testCase.method, testCase.requestPath, testCase.body, cookie, contentType)
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
			if testCase.schema != "" {
				assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), testCase.schema)
			}
			assertInviteSuccessDocumented(t, document, testCase)
		})
	}
}

func TestInviteAcceptanceOpenAPIDocumentsInvalidSession(t *testing.T) {
	document := loadOpenAPI(t)
	h := newAuthHarness(t, nil)
	recorder := acceptInviteRequestWithCookie(t, h, &http.Cookie{Name: sessionCookieName, Value: "unknown"})
	testCase := authContractCase{
		path: "/api/v1/invite/{code}/accept", method: "post", status: http.StatusUnauthorized,
		schema: errorSchema, headers: []string{"Cache-Control", "Set-Cookie", "Vary", "X-Request-ID"},
	}
	assertHandlerContract(t, document, recorder, testCase)
	assertDocumentedResponse(t, document, "post /api/v1/invite/{code}/accept",
		http.StatusUnauthorized, document.Paths[testCase.path][testCase.method].Responses, testCase)
}

func TestCreateInviteAllLibrariesMatchesOpenAPI(t *testing.T) {
	document := loadOpenAPI(t)
	for _, testCase := range []struct {
		name, libraryField string
	}{
		{name: "omitted"},
		{name: "empty", libraryField: `,"library_ids":[]`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := newServiceBackedInviteHarness(t)
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			body := `{"media_server_id":"33333333-3333-4333-8333-333333333333","label":"Friends"` + testCase.libraryField + `}`
			recorder := h.requestWithContentType(t, http.MethodPost, "/api/v1/invites", body, cookie, "application/json")
			if recorder.Code != http.StatusCreated {
				t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
			}
			assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), "#/components/schemas/CreateInviteResponse")
		})
	}
}

func newServiceBackedInviteHarness(t *testing.T) authHarness {
	t.Helper()
	h := newAuthHarness(t, nil)
	store := &serviceBackedInviteStore{}
	service, err := inviteapp.NewService(store, store, serviceBackedInviteServers{}, unusedInviteProvisioners{}, h.clock, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	h.server.inviteReader = service
	h.server.inviteManager = service
	return h
}

func TestEveryInviteErrorResponseMatchesOpenAPI(t *testing.T) {
	document := loadOpenAPI(t)
	tests := inviteErrorContractCases()
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			h := newAuthHarness(t, nil)
			recorder := testCase.run(t, h)
			if recorder.Code != testCase.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, testCase.status, recorder.Body.String())
			}
			assertJSONMatchesSchema(t, document, recorder.Body.Bytes(), errorSchema)
		})
	}
}

type inviteErrorContractCase struct {
	name   string
	status int
	run    func(*testing.T, authHarness) *httptest.ResponseRecorder
}

func inviteErrorContractCases() []inviteErrorContractCase {
	return []inviteErrorContractCase{
		{name: "unauthorized", status: http.StatusUnauthorized, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.request(t, http.MethodGet, "/api/v1/invites", "", nil)
		}},
		{name: "forbidden", status: http.StatusForbidden, run: inviteForbiddenResponse},
		{name: "not found", status: http.StatusNotFound, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			h.invites.err = core.ErrInviteUnavailable
			return h.request(t, http.MethodGet, "/api/v1/invite/"+testInviteCode, "", nil)
		}},
		{name: "username unavailable", status: http.StatusConflict, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			h.invites.err = &core.MediaUserNameError{}
			return acceptInviteRequestForTest(t, h)
		}},
		{name: "provisioning failure leased", status: http.StatusConflict, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			h.invites.err = core.ErrInviteProvisioningFailureLeased
			cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
			return h.request(t, http.MethodDelete,
				"/api/v1/invites/provisioning-failures/55555555-5555-4555-8555-555555555555", "", cookie)
		}},
		{name: "unsupported media type", status: http.StatusUnsupportedMediaType, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.requestWithContentType(t, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept", `{}`, nil, "text/plain")
		}},
		{name: "validation", status: http.StatusUnprocessableEntity, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.requestWithContentType(t, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept", `{}`, nil, "application/json")
		}},
		{name: "rate limited", status: http.StatusTooManyRequests, run: inviteRateLimitedResponse},
		{name: "internal", status: http.StatusInternalServerError, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			h.invites.err = errors.New("failed")
			return acceptInviteRequestForTest(t, h)
		}},
		{name: "media server", status: http.StatusBadGateway, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			h.invites.err = &core.MediaServerError{Kind: core.MediaServerUnavailable, Operation: "create_user"}
			return acceptInviteRequestForTest(t, h)
		}},
		{name: "busy", status: http.StatusServiceUnavailable, run: inviteBusyResponse},
		{name: "method", status: http.StatusMethodNotAllowed, run: func(t *testing.T, h authHarness) *httptest.ResponseRecorder {
			t.Helper()
			return h.request(t, http.MethodDelete, "/api/v1/invite/"+testInviteCode, "", nil)
		}},
	}
}

func inviteForbiddenResponse(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	return crossOriginRequest(h, http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept", `{}`)
}

func inviteRateLimitedResponse(t *testing.T, _ authHarness) *httptest.ResponseRecorder {
	t.Helper()
	h := inviteRateLimitHarness(t, false)
	_ = h.request(t, http.MethodGet, "/api/v1/invite/"+testInviteCode, "", nil)
	return h.request(t, http.MethodGet, "/api/v1/invite/"+testInviteCode, "", nil)
}

func inviteBusyResponse(t *testing.T, h authHarness) *httptest.ResponseRecorder {
	t.Helper()
	for range cap(h.server.inviteAcceptances) {
		h.server.inviteAcceptances <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(h.server.inviteAcceptances) {
			<-h.server.inviteAcceptances
		}
	})
	return acceptInviteRequestForTest(t, h)
}

func assertInviteSuccessDocumented(t *testing.T, document openAPIDocument, testCase struct {
	name, method, specPath, requestPath, body, schema string
	status                                            int
	public                                            bool
},
) {
	t.Helper()
	operation := document.Paths[testCase.specPath][strings.ToLower(testCase.method)]
	response := resolveOpenAPIResponse(t, document, operation.Responses[strconv.Itoa(testCase.status)])
	if testCase.schema == "" {
		if len(response.Content) != 0 {
			t.Fatal("no-content response documents a body")
		}
		return
	}
	if got := response.Content["application/json"].Schema.Ref; got != testCase.schema {
		t.Fatalf("documented schema = %q, want %q", got, testCase.schema)
	}
}

func TestInviteAcceptanceSecretsNeverReachResponseLogsAuditOrMetrics(t *testing.T) {
	const password = "Th1s-is-a-unique-password!"
	base := newAuthHarness(t, nil)
	store := &serviceBackedInviteStore{invite: newFakeInviteService(base.clock.Now()).invite}
	provisioner := &panicInviteProvisioner{code: testInviteCode}
	var applicationLog, auditLog strings.Builder
	applicationLogger := slog.New(slog.NewJSONHandler(&applicationLog, nil))
	service, err := inviteapp.NewService(
		store, store, serviceBackedInviteServers{}, panicInviteAcquirer{provisioner},
		base.clock, applicationLogger,
	)
	if err != nil {
		t.Fatal(err)
	}
	metrics := telemetry.NewPromMetrics("secrecytest")
	audit := telemetry.NewAuditLogger(&auditLog, base.clock)
	server := New(config.HTTPConfig{
		Addr: ":0", ReadHeaderTimeout: time.Second, WriteTimeout: time.Second, MaxBodyBytes: 8192,
	}, Deps{
		Logger: applicationLogger, Metrics: metrics,
		Readiness: telemetry.NewReadiness(true), Pinger: &fakePinger{},
		Identity: core.NewLocalIdentityProvider(base.store), Accounts: base.store,
		Authorizer: base.authorization, Roles: base.authorization,
		InviteReader: service, InviteManager: service, Sessions: base.sessions, Audit: audit,
		Clock: base.clock, Auth: newAuthHarnessConfig(nil),
		AuditCorrelationKey: []byte("0123456789abcdef0123456789abcdef"),
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/invite/"+testInviteCode+"/accept",
		strings.NewReader(`{"username":"new-user","password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !provisioner.created || !provisioner.deleted {
		t.Fatalf("panic acceptance = %d created=%v deleted=%v: %s",
			response.Code, provisioner.created, provisioner.deleted, response.Body.String())
	}
	metricsRecorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsRecorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	outputs := []string{response.Body.String(), applicationLog.String(), auditLog.String(), metricsRecorder.Body.String()}
	for index, output := range outputs {
		if strings.Contains(output, password) || strings.Contains(output, testInviteCode) {
			t.Fatalf("secret found in output %d: %s", index, output)
		}
	}
}
