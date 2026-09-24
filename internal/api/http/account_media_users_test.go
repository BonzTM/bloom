package http

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/telemetry"
)

type fakeAccountMediaUsers struct {
	mu          sync.Mutex
	links       []core.AccountMediaUser
	err         error
	ensureCalls int
}

func (f *fakeAccountMediaUsers) EnsureLinks(
	_ context.Context, _ core.Account, serverID string,
) ([]core.AccountMediaUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCalls++
	links := slices.Clone(f.links)
	if serverID == "" {
		return links, f.err
	}
	for _, link := range links {
		if link.MediaServerID == serverID {
			return []core.AccountMediaUser{link}, f.err
		}
	}
	return []core.AccountMediaUser{}, f.err
}

func (f *fakeAccountMediaUsers) List(context.Context, string) ([]core.AccountMediaUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.links), f.err
}

func (f *fakeAccountMediaUsers) Set(
	_ context.Context, accountID, serverID, mediaUserID string,
) (core.AccountMediaUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return core.AccountMediaUser{}, f.err
	}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	link := core.AccountMediaUser{
		AccountID: accountID, MediaServerID: serverID, MediaServerName: "Home",
		MediaUserID: mediaUserID, Username: "alice", Source: core.AccountMediaUserSourceAdmin,
		CreatedAt: now, UpdatedAt: now,
	}
	f.links = []core.AccountMediaUser{link}
	return link, nil
}

func (f *fakeAccountMediaUsers) Delete(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if len(f.links) == 0 {
		return core.ErrNotFound
	}
	f.links = nil
	return nil
}

func TestOwnMediaUserAndStatsRoutes(t *testing.T) {
	h := newAuthHarness(t, nil)
	link := testAccountMediaUser()
	h.accountMediaUsers.links = []core.AccountMediaUser{link}
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	listed := h.request(t, http.MethodGet, "/api/v1/me/media-users", "", cookie)
	if listed.Code != http.StatusOK {
		t.Fatalf("list own links = %d: %s", listed.Code, listed.Body.String())
	}
	stats := h.request(t, http.MethodGet, "/api/v1/stats/me?days=7", "", cookie)
	if stats.Code != http.StatusOK {
		t.Fatalf("own stats = %d: %s", stats.Code, stats.Body.String())
	}
	var body statsUserDetailResponse
	if err := json.Unmarshal(stats.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Window.MediaServerID != link.MediaServerID || body.Window.MediaUserID != link.MediaUserID {
		t.Fatalf("own stats window = %+v", body.Window)
	}
	h.stats.mu.Lock()
	query := h.stats.queries[len(h.stats.queries)-1]
	h.stats.mu.Unlock()
	if query.UserServerID != link.MediaServerID || query.MediaUserID != link.MediaUserID {
		t.Fatalf("own stats query = %+v", query)
	}
}

func TestOwnStatsRequiresOwnPermissionAndLinkedUser(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	h.authorization.mu.Lock()
	h.authorization.permissions[h.store.accounts["alice"].ID] = []core.Permission{core.PermissionStatsReadAll}
	h.authorization.mu.Unlock()
	forbidden := h.request(t, http.MethodGet, "/api/v1/stats/me", "", cookie)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("stats.read.all only = %d", forbidden.Code)
	}
	h.authorization.mu.Lock()
	h.authorization.permissions[h.store.accounts["alice"].ID] = []core.Permission{core.PermissionStatsReadOwn}
	h.authorization.mu.Unlock()
	missing := h.request(t, http.MethodGet, "/api/v1/stats/me", "", cookie)
	if missing.Code != http.StatusNotFound || decodeEnvelope(t, missing).Code != codeMediaUserNotLinked {
		t.Fatalf("unlinked own stats = %d: %s", missing.Code, missing.Body.String())
	}
}

func TestAdminAccountMediaUserRoutesValidateAndAudit(t *testing.T) {
	h := newAuthHarness(t, nil)
	cookie := sessionCookie(t, h.login(t, "alice", "secret-password"))
	path := "/api/v1/accounts/11111111-1111-4111-8111-111111111111/media-users/33333333-3333-4333-8333-333333333333"
	invalid := h.requestWithContentType(t, http.MethodPut, path, `{"media_user_id":"bad\u0000id"}`, cookie, "application/json")
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid PUT = %d: %s", invalid.Code, invalid.Body.String())
	}
	if event := h.audit.last(t); event.Action != "account_media_user.set" || event.Result != telemetry.AuditFailure {
		t.Fatalf("invalid set audit = %+v", event)
	}
	put := h.requestWithContentType(t, http.MethodPut, path, `{"media_user_id":"media-user-1"}`, cookie, "application/json")
	if put.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", put.Code, put.Body.String())
	}
	if event := h.audit.last(t); event.Action != "account_media_user.set" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("set audit = %+v", event)
	}
	listed := h.request(t, http.MethodGet, "/api/v1/accounts/11111111-1111-4111-8111-111111111111/media-users", "", cookie)
	if listed.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", listed.Code, listed.Body.String())
	}
	deleted := h.request(t, http.MethodDelete, path, "", cookie)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d: %s", deleted.Code, deleted.Body.String())
	}
	if event := h.audit.last(t); event.Action != "account_media_user.delete" || event.Result != telemetry.AuditSuccess {
		t.Fatalf("delete audit = %+v", event)
	}
}

func testAccountMediaUser() core.AccountMediaUser {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	return core.AccountMediaUser{
		AccountID:     "11111111-1111-4111-8111-111111111111",
		MediaServerID: "33333333-3333-4333-8333-333333333333", MediaServerName: "Home",
		MediaUserID: "media-user-1", Username: "alice", Source: core.AccountMediaUserSourceMatch,
		CreatedAt: now, UpdatedAt: now,
	}
}
