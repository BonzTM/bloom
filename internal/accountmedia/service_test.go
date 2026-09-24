package accountmedia_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/accountmedia"
	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

type linkStore struct{ links []core.AccountMediaUser }

func (s *linkStore) GetAccountMediaUser(_ context.Context, accountID, serverID string) (core.AccountMediaUser, error) {
	for _, link := range s.links {
		if link.AccountID == accountID && link.MediaServerID == serverID && link.SuppressedAt == nil {
			return link, nil
		}
	}
	return core.AccountMediaUser{}, core.ErrNotFound
}

func (s *linkStore) ListAccountMediaUsers(
	_ context.Context, accountID string, includeSuppressed bool, limit int,
) ([]core.AccountMediaUser, error) {
	result := make([]core.AccountMediaUser, 0, limit)
	for _, link := range s.links {
		if link.AccountID == accountID && (includeSuppressed || link.SuppressedAt == nil) && len(result) < limit {
			result = append(result, link)
		}
	}
	return result, nil
}

func (s *linkStore) SetAccountMediaUser(_ context.Context, link core.AccountMediaUser) error {
	for index := range s.links {
		if s.links[index].AccountID == link.AccountID && s.links[index].MediaServerID == link.MediaServerID {
			s.links[index] = link
			return nil
		}
	}
	s.links = append(s.links, link)
	return nil
}

func (s *linkStore) CreateAccountMediaUserIfAbsent(_ context.Context, link core.AccountMediaUser) (bool, error) {
	for _, existing := range s.links {
		if existing.AccountID == link.AccountID && existing.MediaServerID == link.MediaServerID ||
			existing.MediaServerID == link.MediaServerID && existing.MediaUserID == link.MediaUserID {
			return false, nil
		}
	}
	s.links = append(s.links, link)
	return true, nil
}

func (s *linkStore) SuppressAccountMediaUser(
	_ context.Context, accountID, serverID string, suppressedAt time.Time,
) error {
	for index, link := range s.links {
		if link.AccountID == accountID && link.MediaServerID == serverID && link.SuppressedAt == nil {
			s.links[index].SuppressedAt = &suppressedAt
			s.links[index].UpdatedAt = suppressedAt
			return nil
		}
	}
	return core.ErrNotFound
}

func TestDeleteSuppressesAutomaticMatchUntilAdminSet(t *testing.T) {
	service, store, servers, _ := newService(t)
	account := core.Account{ID: testID(20), Username: "alice"}
	serverID := testID(1)
	servers.servers = []core.MediaServerConnection{{Server: core.MediaServer{ID: serverID, Name: "Home"}}}
	servers.users[serverID] = core.MediaUser{ID: "media-user", Name: "alice"}
	if _, err := service.EnsureLinks(t.Context(), account, serverID); err != nil {
		t.Fatalf("initial EnsureLinks: %v", err)
	}
	if err := service.Delete(t.Context(), account.ID, serverID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	links, err := service.EnsureLinks(t.Context(), account, serverID)
	if err != nil || len(links) != 0 || store.links[0].SuppressedAt == nil {
		t.Fatalf("EnsureLinks after delete = %+v, %v; stored %+v", links, err, store.links)
	}
	if _, err := service.Set(t.Context(), account.ID, serverID, "media-user"); err != nil {
		t.Fatalf("Set after delete: %v", err)
	}
	if store.links[0].SuppressedAt != nil {
		t.Fatalf("Set retained suppression: %+v", store.links[0])
	}
}

type accountStore struct{ account core.Account }

func (s accountStore) CreateAccount(context.Context, core.Account) error { return nil }
func (s accountStore) GetAccount(_ context.Context, id string) (core.Account, error) {
	if id != s.account.ID {
		return core.Account{}, core.ErrNotFound
	}
	return s.account, nil
}

func (s accountStore) GetAccountByUsername(context.Context, string) (core.Account, error) {
	return s.account, nil
}

type mediaServers struct {
	servers     []core.MediaServerConnection
	users       map[string]core.MediaUser
	lookupErr   error
	lookupCalls map[string]int
}

func (s *mediaServers) List(context.Context, string, int) ([]core.MediaServerConnection, error) {
	return slices.Clone(s.servers), nil
}

func (s *mediaServers) Get(_ context.Context, id string) (core.MediaServerConnection, error) {
	for _, server := range s.servers {
		if server.Server.ID == id {
			return server, nil
		}
	}
	return core.MediaServerConnection{}, core.ErrNotFound
}

func (s *mediaServers) FindUserByName(_ context.Context, id, _ string) (core.MediaUser, bool, bool, error) {
	s.lookupCalls[id]++
	if s.lookupErr != nil {
		return core.MediaUser{}, false, true, s.lookupErr
	}
	user, found := s.users[id]
	return user, found, true, nil
}

func (s *mediaServers) FindUserByID(_ context.Context, id, mediaUserID string) (core.MediaUser, bool, bool, error) {
	user, found := s.users[id]
	return user, found && user.ID == mediaUserID, true, s.lookupErr
}

type matchMetrics struct{ outcomes []string }

func (m *matchMetrics) IncMediaUserMatch(outcome string) { m.outcomes = append(m.outcomes, outcome) }

func TestEnsureLinksMatchesAtMostEightServersAndIgnoresLookupFailure(t *testing.T) {
	service, store, servers, metrics := newService(t)
	for index := range 9 {
		id := testID(index + 1)
		servers.servers = append(servers.servers, core.MediaServerConnection{Server: core.MediaServer{ID: id, Name: id}})
		servers.users[id] = core.MediaUser{ID: "user-" + id, Name: "alice"}
	}
	servers.lookupErr = errors.New("offline")
	links, err := service.EnsureLinks(context.Background(), core.Account{ID: testID(20), Username: "alice"}, "")
	if err != nil || len(links) != 0 || len(store.links) != 0 {
		t.Fatalf("EnsureLinks = %+v, %v", links, err)
	}
	if len(servers.lookupCalls) != accountmedia.MaxMatchServersPerRequest || len(metrics.outcomes) != accountmedia.MaxMatchServersPerRequest {
		t.Fatalf("lookup calls = %d metrics = %v", len(servers.lookupCalls), metrics.outcomes)
	}
}

func TestEnsureLinksPersistsMatchAndAdminSetVerifiesUser(t *testing.T) {
	service, store, servers, _ := newService(t)
	account := core.Account{ID: testID(20), Username: "alice"}
	serverID := testID(1)
	servers.servers = []core.MediaServerConnection{{Server: core.MediaServer{ID: serverID, Name: "Home"}}}
	servers.users[serverID] = core.MediaUser{ID: "media-user", Name: "alice"}
	links, err := service.EnsureLinks(context.Background(), account, serverID)
	if err != nil || len(links) != 1 || links[0].Source != core.AccountMediaUserSourceMatch {
		t.Fatalf("EnsureLinks = %+v, %v", links, err)
	}
	admin, err := service.Set(context.Background(), account.ID, serverID, "media-user")
	if err != nil || admin.Source != core.AccountMediaUserSourceAdmin || store.links[0].Username != "alice" {
		t.Fatalf("Set = %+v, %v", admin, err)
	}
	if _, err := service.Set(context.Background(), account.ID, serverID, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Set missing media user = %v", err)
	}
}

func TestEnsureLinksLeavesAmbiguousUsernameUnlinked(t *testing.T) {
	service, store, servers, metrics := newService(t)
	serverID := testID(1)
	servers.servers = []core.MediaServerConnection{{Server: core.MediaServer{ID: serverID, Name: "Home"}}}
	servers.lookupErr = core.ErrMediaUserAmbiguous
	links, err := service.EnsureLinks(context.Background(), core.Account{ID: testID(20), Username: "alice"}, serverID)
	if err != nil || len(links) != 0 || len(store.links) != 0 {
		t.Fatalf("EnsureLinks = %+v, %v; stored %+v", links, err, store.links)
	}
	if !slices.Equal(metrics.outcomes, []string{"ambiguous"}) {
		t.Fatalf("match outcomes = %v, want [ambiguous]", metrics.outcomes)
	}
}

func newService(t *testing.T) (*accountmedia.Service, *linkStore, *mediaServers, *matchMetrics) {
	t.Helper()
	store := &linkStore{}
	servers := &mediaServers{users: make(map[string]core.MediaUser), lookupCalls: make(map[string]int)}
	metrics := &matchMetrics{}
	account := core.Account{ID: testID(20), Username: "alice"}
	service, err := accountmedia.NewService(accountmedia.Dependencies{
		Reader: store, Writer: store, Accounts: accountStore{account: account}, Servers: servers,
		Clock:  testutil.NewFakeClock(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)),
		Logger: slog.New(slog.DiscardHandler), Metrics: metrics,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, servers, metrics
}

func testID(value int) string { return "00000000-0000-4000-8000-" + fmt.Sprintf("%012d", value) }
