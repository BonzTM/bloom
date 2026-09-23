package http

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
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
}

type serviceBackedInviteStore struct{ invite core.Invite }

func (s *serviceBackedInviteStore) CreateInvite(
	_ context.Context, value core.Invite, _ [sha256.Size]byte,
) error {
	s.invite = value
	return nil
}

func (s *serviceBackedInviteStore) RevokeInvite(context.Context, string, time.Time) (core.Invite, error) {
	return s.invite, nil
}

func (*serviceBackedInviteStore) RedeemInvite(
	context.Context, [sha256.Size]byte, core.Clock, core.InviteRedeemFunc,
) error {
	return nil
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
	context.Context, [sha256.Size]byte,
) (core.Invite, error) {
	return s.invite, nil
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

type unusedInviteProvisioners struct{}

func (unusedInviteProvisioners) AcquireUserProvisioner(
	context.Context, string,
) (core.MediaUserProvisioner, func(), error) {
	return nil, func() {}, nil
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

func (f *fakeInviteService) Accept(ctx context.Context, _, username, _ string) (inviteapp.Accepted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accepted++
	_, f.deadline = ctx.Deadline()
	return inviteapp.Accepted{InviteID: f.invite.ID, MediaServerName: "Home", Username: username}, f.err
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
	service, err := inviteapp.NewService(store, store, serviceBackedInviteServers{}, unusedInviteProvisioners{}, h.clock)
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

func TestInviteResponsesNeverContainPasswordOrCodeAfterCreation(t *testing.T) {
	h := newAuthHarness(t, nil)
	response := acceptInviteRequestForTest(t, h)
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode acceptance: %v", err)
	}
	if _, present := value["password"]; present {
		t.Fatal("acceptance response contains password")
	}
	if _, present := value["code"]; present {
		t.Fatal("acceptance response contains invite code")
	}
}
