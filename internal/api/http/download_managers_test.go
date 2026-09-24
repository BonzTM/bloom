package http

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type fakeDownloadManagerService struct {
	mu           sync.Mutex
	managers     []core.DownloadManager
	connection   core.DownloadManagerConnection
	options      core.DownloadManagerOptions
	err          error
	registered   int
	deleted      int
	lastAPIKey   string
	lastAfter    string
	lastPageSize int
}

func newFakeDownloadManagerService() *fakeDownloadManagerService {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	manager := core.DownloadManager{
		ID: "33333333-3333-4333-8333-333333333333", Kind: core.DownloadManagerKindRadarr,
		Name: "Main Radarr", BaseURL: "https://radarr.example.test", CreatedAt: now, UpdatedAt: now,
	}
	options := core.DownloadManagerOptions{
		QualityProfiles: []core.DownloadManagerOption{{ID: "1", Name: "HD"}},
		RootFolders:     []core.DownloadManagerOption{{ID: "/movies", Name: "/movies"}},
		Tags:            []core.DownloadManagerOption{{ID: "2", Name: "requested"}},
	}
	return &fakeDownloadManagerService{
		managers: []core.DownloadManager{manager}, options: options,
		connection: core.DownloadManagerConnection{Manager: manager, Info: core.DownloadManagerInfo{
			Name: "Main Radarr", Version: "5.0", Capabilities: core.DownloadManagerCapabilities{Kinds: []core.MediaKind{core.MediaKindMovie}},
		}},
	}
}

func (f *fakeDownloadManagerService) Register(
	_ context.Context, _ core.DownloadManagerKind, _, _, apiKey string, _ bool,
) (core.DownloadManagerConnection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered++
	f.lastAPIKey = apiKey
	return f.connection, f.err
}

func (f *fakeDownloadManagerService) List(_ context.Context, after string, pageSize int) ([]core.DownloadManager, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastAfter, f.lastPageSize = after, pageSize
	return append([]core.DownloadManager(nil), f.managers...), f.err
}

func (f *fakeDownloadManagerService) Options(context.Context, string) (core.DownloadManagerOptions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.options, f.err
}

func (f *fakeDownloadManagerService) Delete(context.Context, string) (core.DownloadManager, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted++
	return f.connection.Manager, f.err
}

func TestDownloadManagerRoutesAndCredentialSecrecy(t *testing.T) {
	const apiKey = "download-manager-super-secret"
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	create := h.requestWithContentType(t, http.MethodPost, "/api/v1/download-managers",
		`{"kind":"radarr","name":"Main Radarr","base_url":"https://radarr.example.test","api_key":"`+apiKey+`","allow_insecure":false}`,
		cookie, "application/json")
	if create.Code != http.StatusCreated || strings.Contains(create.Body.String(), apiKey) || strings.Contains(h.logs.String(), apiKey) {
		t.Fatalf("create = %d %s logs=%s", create.Code, create.Body.String(), h.logs.String())
	}
	if h.downloadManagers.lastAPIKey != apiKey {
		t.Fatalf("adapter credential = %q", h.downloadManagers.lastAPIKey)
	}
	if event := h.audit.last(t); event.Action != "download_manager.create" || event.Resource != "download_manager:33333333-3333-4333-8333-333333333333" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("create audit = %+v", event)
	}
	for _, testCase := range []struct {
		method, path string
		want         int
	}{
		{method: http.MethodGet, path: "/api/v1/download-managers", want: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/download-managers/33333333-3333-4333-8333-333333333333/options", want: http.StatusOK},
		{method: http.MethodDelete, path: "/api/v1/download-managers/33333333-3333-4333-8333-333333333333", want: http.StatusNoContent},
	} {
		recorder := h.request(t, testCase.method, testCase.path, "", cookie)
		if recorder.Code != testCase.want || strings.Contains(recorder.Body.String(), apiKey) {
			t.Errorf("%s %s = %d %s", testCase.method, testCase.path, recorder.Code, recorder.Body.String())
		}
	}
	if event := h.audit.last(t); event.Action != "download_manager.delete" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("delete audit = %+v", event)
	}
}

func TestDownloadManagerRoutesRejectInvalidAndUpstreamFailures(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	invalid := h.requestWithContentType(t, http.MethodPost, "/api/v1/download-managers",
		`{"kind":"invalid","name":"","base_url":"ftp://bad","api_key":""}`, cookie, "application/json")
	if invalid.Code != http.StatusUnprocessableEntity || h.downloadManagers.registered != 0 {
		t.Fatalf("invalid create = %d registered=%d", invalid.Code, h.downloadManagers.registered)
	}
	h.downloadManagers.err = &core.DownloadManagerError{
		Kind: core.DownloadManagerUnavailable, Operation: "probe", Retryable: true, RetryAfter: 45 * time.Second,
	}
	failed := h.requestWithContentType(t, http.MethodPost, "/api/v1/download-managers",
		`{"kind":"radarr","name":"Main","base_url":"https://radarr.example.test","api_key":"secret","allow_insecure":false}`,
		cookie, "application/json")
	if failed.Code != http.StatusServiceUnavailable || failed.Header().Get("Retry-After") != "30" ||
		!strings.Contains(failed.Body.String(), codeDownloadManagerFailure) {
		t.Fatalf("failed create = %d %s", failed.Code, failed.Body.String())
	}
	h.downloadManagers.err = core.ErrDownloadManagerInUse
	deleted := h.request(t, http.MethodDelete, "/api/v1/download-managers/33333333-3333-4333-8333-333333333333", "", cookie)
	if deleted.Code != http.StatusConflict || !strings.Contains(deleted.Body.String(), codeDownloadManagerInUse) {
		t.Fatalf("in-use delete = %d %s", deleted.Code, deleted.Body.String())
	}
	if errors.Is(h.downloadManagers.err, core.ErrDownloadManagerInUse) && strings.Contains(h.logs.String(), "secret") {
		t.Fatal("application log contains the API key")
	}
}
