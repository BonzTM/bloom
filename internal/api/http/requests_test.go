package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/httputil"
	"github.com/BonzTM/bloom/internal/metadata/tmdb"
	requestapp "github.com/BonzTM/bloom/internal/request"
	"github.com/BonzTM/bloom/internal/telemetry"
	"github.com/BonzTM/bloom/internal/testutil"
)

const (
	testRequestAccountID = "11111111-1111-4111-8111-111111111111"
	testRequestProfileID = "22222222-2222-4222-8222-222222222222"
)

func TestCreateRequestAutoApprovalEmitsCreateAndApproveAudits(t *testing.T) {
	audit := &recordingAudit{}
	provider := &staticRequestMetadata{movie: validRequestMovie()}
	service := newHandlerRequestService(t, provider)
	server := newRequestHandlerServer(service, audit)
	body := `{"kind":"movie","provider_id":"11","profile_id":"` + testRequestProfileID + `","seasons":[]}`
	request := requestWithAccount(t, http.MethodPost, "/api/v1/requests", body, core.PermissionRequestsApprove)
	recorder := httptest.NewRecorder()
	server.handleCreateRequest(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	events := audit.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit event count = %d, want 2", len(events))
	}
	if events[0].Action != "request.create" || events[1].Action != "request.approve" {
		t.Fatalf("audit actions = %q, %q", events[0].Action, events[1].Action)
	}
	if events[0].Actor != testRequestAccountID || events[1].Actor != testRequestAccountID || events[0].Resource != events[1].Resource {
		t.Fatalf("audit events = %+v", events)
	}
	if events[0].RequestID != "request-1" || events[1].RequestID != "request-1" ||
		events[0].Result != telemetry.AuditSuccess || events[1].Result != telemetry.AuditSuccess {
		t.Fatalf("audit results = %+v", events)
	}
}

func TestFailedQuotaDeletionIsAudited(t *testing.T) {
	cases := []struct {
		name, path, action, resource string
		handle                       func(*Server, http.ResponseWriter, *http.Request)
	}{
		{
			name: "role", path: "/api/v1/roles/" + testRequestAccountID + "/request-quota",
			action: "request_quota.role.delete", resource: "role:" + testRequestAccountID,
			handle: (*Server).handleDeleteRoleRequestQuota,
		},
		{
			name: "account", path: "/api/v1/accounts/" + testRequestAccountID + "/request-quota",
			action: "request_quota.account.delete", resource: "account:" + testRequestAccountID,
			handle: (*Server).handleDeleteAccountRequestQuota,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			audit := &recordingAudit{}
			store := &requestHandlerStore{quotaErr: core.ErrNotFound}
			server := newRequestHandlerServer(newQuotaHandlerService(t, store), audit)
			request := requestWithAccount(t, http.MethodDelete, tc.path, "", core.PermissionAdminSettings)
			request.SetPathValue("id", testRequestAccountID)
			recorder := httptest.NewRecorder()
			tc.handle(server, recorder, request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			events := audit.snapshot()
			if len(events) != 1 {
				t.Fatalf("audit event count = %d, want 1", len(events))
			}
			event := events[0]
			if event.Action != tc.action || event.Resource != tc.resource || event.Result != telemetry.AuditFailure ||
				event.Actor != testRequestAccountID || event.RequestID != "request-1" {
				t.Fatalf("audit event = %+v", event)
			}
		})
	}
}

func newQuotaHandlerService(t *testing.T, store *requestHandlerStore) *requestapp.Service {
	t.Helper()
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	service, err := requestapp.NewService(requestapp.Dependencies{
		Profiles: store, ProfileWriter: store, Requests: store, RequestWriter: store,
		QuotaReader: store, QuotaWriter: store, QuotaDeleter: store,
		Metadata: &staticRequestMetadata{movie: validRequestMovie()}, Clock: clock,
	})
	if err != nil {
		t.Fatalf("new request service: %v", err)
	}
	return service
}

func TestMalformedTMDBTitleReturnsOpaqueProviderFailure(t *testing.T) {
	client := malformedTMDBClient(t)
	t.Run("detail route", func(t *testing.T) {
		server := &Server{logger: slog.New(slog.DiscardHandler), metadataReader: client}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/metadata/movies/11", nil)
		request.SetPathValue("id", "11")
		recorder := httptest.NewRecorder()
		server.handleMetadataMovie(recorder, request)
		assertMetadataFailure(t, recorder)
	})
	t.Run("create route", func(t *testing.T) {
		service := newHandlerRequestService(t, client)
		server := newRequestHandlerServer(service, &recordingAudit{})
		body := `{"kind":"movie","provider_id":"11","profile_id":"` + testRequestProfileID + `","seasons":[]}`
		request := requestWithAccount(t, http.MethodPost, "/api/v1/requests", body, core.PermissionRequestsCreate)
		recorder := httptest.NewRecorder()
		server.handleCreateRequest(recorder, request)
		assertMetadataFailure(t, recorder)
	})
}

func malformedTMDBClient(t *testing.T) *tmdb.Client {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"id":11,"title":" "}`)); err != nil {
			t.Errorf("write TMDB response: %v", err)
		}
	}))
	t.Cleanup(upstream.Close)
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	client, err := tmdb.New("test-key", tmdb.Dependencies{BaseURL: upstream.URL, Clock: clock})
	if err != nil {
		t.Fatalf("new TMDB client: %v", err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func assertMetadataFailure(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	var response httputil.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if recorder.Code != http.StatusBadGateway || response.Code != codeMetadataProviderFailure {
		t.Fatalf("response = %d %+v", recorder.Code, response)
	}
	if response.Message != http.StatusText(http.StatusBadGateway) || strings.Contains(response.Message, core.ErrInvalidArgument.Error()) {
		t.Fatalf("response message = %q", response.Message)
	}
}

func requestWithAccount(t *testing.T, method, path, body string, permissions ...core.Permission) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(request.Context(), accountKey, core.Account{ID: testRequestAccountID})
	ctx = context.WithValue(ctx, permissionsKey, permissions)
	ctx = context.WithValue(ctx, requestIDKey, "request-1")
	return request.WithContext(ctx)
}

func newRequestHandlerServer(service *requestapp.Service, audit *recordingAudit) *Server {
	return &Server{
		logger: slog.New(slog.DiscardHandler), maxBodyBytes: 8192, requestService: service,
		audit: audit, auditFailureMetrics: telemetry.NopMetrics{},
	}
}

func newHandlerRequestService(t *testing.T, provider core.MetadataProvider) *requestapp.Service {
	t.Helper()
	store := &requestHandlerStore{profile: core.RequestProfile{ID: testRequestProfileID, Kinds: []core.MediaKind{core.MediaKindMovie}}}
	clock := testutil.NewFakeClock(time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC))
	service, err := requestapp.NewService(requestapp.Dependencies{
		Profiles: store, ProfileWriter: store, Requests: store, RequestWriter: store,
		QuotaReader: store, QuotaWriter: store, QuotaDeleter: store, Metadata: provider, Clock: clock,
	})
	if err != nil {
		t.Fatalf("new request service: %v", err)
	}
	return service
}

type staticRequestMetadata struct {
	movie core.MetadataTitle
	err   error
}

func (m *staticRequestMetadata) Search(context.Context, core.MetadataSearch) ([]core.MetadataTitle, error) {
	return nil, m.err
}

func (m *staticRequestMetadata) Movie(context.Context, string) (core.MetadataTitle, error) {
	return m.movie, m.err
}

func (m *staticRequestMetadata) Series(context.Context, string, bool) (core.MetadataSeries, error) {
	return core.MetadataSeries{}, m.err
}

func validRequestMovie() core.MetadataTitle {
	return core.MetadataTitle{
		Kind: core.MediaKindMovie, Provider: core.MetadataProviderTMDB, ProviderID: "11", Title: "Film", Year: 2026,
	}
}

type requestHandlerStore struct {
	profile core.RequestProfile
	created core.MediaRequest
	// quotaErr is what every quota delete answers with; nil means success.
	quotaErr error
}

func (s *requestHandlerStore) GetRequestProfile(context.Context, string) (core.RequestProfile, error) {
	return s.profile, nil
}

func (*requestHandlerStore) ListRequestProfiles(context.Context, string, int) ([]core.RequestProfile, error) {
	return nil, nil
}

func (*requestHandlerStore) CreateRequestProfile(context.Context, core.RequestProfile) error {
	return nil
}

func (*requestHandlerStore) UpdateRequestProfile(context.Context, core.RequestProfile) error {
	return nil
}

func (*requestHandlerStore) DeleteRequestProfile(context.Context, string) error { return nil }

func (s *requestHandlerStore) GetRequest(context.Context, string) (core.MediaRequest, error) {
	return s.created, nil
}

func (*requestHandlerStore) ListRequests(context.Context, core.RequestListFilter) ([]core.MediaRequest, error) {
	return nil, nil
}

func (s *requestHandlerStore) CreateRequest(_ context.Context, request core.MediaRequest, _ time.Time, _ bool) error {
	if err := core.ValidateMediaRequest(request); err != nil {
		return err
	}
	s.created = request
	return nil
}

func (*requestHandlerStore) TransitionRequest(
	context.Context, string, core.RequestStatus, core.RequestStatus, string, string, time.Time,
) (core.MediaRequest, error) {
	return core.MediaRequest{}, errors.New("unexpected request transition")
}

func (*requestHandlerStore) GetRoleRequestQuota(context.Context, string) (core.RoleRequestQuota, error) {
	return core.RoleRequestQuota{}, core.ErrNotFound
}

func (*requestHandlerStore) GetAccountRequestQuota(context.Context, string) (core.AccountRequestQuota, error) {
	return core.AccountRequestQuota{}, core.ErrNotFound
}

func (*requestHandlerStore) SetRoleRequestQuota(context.Context, core.RoleRequestQuota) error {
	return nil
}

func (s *requestHandlerStore) DeleteRoleRequestQuota(context.Context, string) error {
	return s.quotaErr
}

func (*requestHandlerStore) SetAccountRequestQuota(context.Context, core.AccountRequestQuota) error {
	return nil
}

func (s *requestHandlerStore) DeleteAccountRequestQuota(context.Context, string) error {
	return s.quotaErr
}
