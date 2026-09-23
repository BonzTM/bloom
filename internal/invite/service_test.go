package invite_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/invite"
	"github.com/BonzTM/bloom/internal/testutil"
)

type fakeStore struct {
	invite      core.Invite
	createHash  [sha256.Size]byte
	createErr   error
	redeemErr   error
	failCommit  error
	redemptions []core.InviteRedemption
	failures    []core.InviteProvisioningFailure
	failureErr  error
}

func (s *fakeStore) CreateInvite(_ context.Context, value core.Invite, hash [sha256.Size]byte) error {
	s.invite, s.createHash = value, hash
	return s.createErr
}

func (s *fakeStore) RevokeInvite(_ context.Context, _ string, at time.Time) (core.Invite, error) {
	s.invite.RevokedAt = &at
	return s.invite, nil
}

func (s *fakeStore) RedeemInvite(
	ctx context.Context, _ [sha256.Size]byte, _ core.Clock, redeem core.InviteRedeemFunc,
) error {
	if s.redeemErr != nil {
		return s.redeemErr
	}
	redemption, err := redeem(ctx, s.invite)
	if err != nil {
		if provisioningErr, ok := errors.AsType[*core.InviteProvisioningError](err); ok && provisioningErr != nil {
			s.failures = append(s.failures, provisioningErr.Failure)
			return errors.Join(err, s.failureErr)
		}
		return err
	}
	s.redemptions = append(s.redemptions, redemption)
	return s.failCommit
}

func (s *fakeStore) RecordInviteProvisioningFailure(
	_ context.Context, failure core.InviteProvisioningFailure,
) error {
	s.failures = append(s.failures, failure)
	return s.failureErr
}

func (s *fakeStore) GetInvite(_ context.Context, _ string) (core.Invite, error) { return s.invite, nil }

func (s *fakeStore) GetInviteByCodeHash(_ context.Context, _ [sha256.Size]byte) (core.Invite, error) {
	return s.invite, nil
}

func (s *fakeStore) ListInvites(context.Context, *core.InviteCursor, int) ([]core.Invite, error) {
	return []core.Invite{s.invite}, nil
}

type fakeServers struct {
	connection core.MediaServerConnection
	libraries  []core.Library
}

func (s fakeServers) Get(context.Context, string) (core.MediaServerConnection, error) {
	return s.connection, nil
}

func (s fakeServers) Libraries(context.Context, string) ([]core.Library, error) {
	return slices.Clone(s.libraries), nil
}

type fakeProvisioner struct {
	createErr, policyErr, deleteErr error
	created, policies, deleted      int
	lastPassword                    string
	deleteContextCanceled           bool
}

func (p *fakeProvisioner) CreateUser(_ context.Context, name, password string) (core.MediaUser, error) {
	p.created++
	p.lastPassword = password
	return core.MediaUser{ID: "55555555-5555-4555-8555-555555555555", Name: name}, p.createErr
}

func (p *fakeProvisioner) SetLibraryAccess(context.Context, string, []string, bool) error {
	p.policies++
	return p.policyErr
}

func (p *fakeProvisioner) DeleteUser(ctx context.Context, _ string) error {
	p.deleted++
	p.deleteContextCanceled = ctx.Err() != nil
	return p.deleteErr
}

type fakeAcquirer struct{ provisioner *fakeProvisioner }

func (a fakeAcquirer) AcquireUserProvisioner(context.Context, string) (core.MediaUserProvisioner, func(), error) {
	return a.provisioner, func() {}, nil
}

func TestCreateValidatesSelectedLibraries(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	servers := fakeServers{
		connection: core.MediaServerConnection{Server: core.MediaServer{ID: testServerID(), Name: "Home"}},
		libraries:  []core.Library{{ID: "11111111-1111-4111-8111-111111111111", Name: "Movies"}},
	}
	service := newService(t, store, servers, &fakeProvisioner{}, now)
	input := invite.CreateInput{
		MediaServerID: testServerID(), CreatedBy: testAccountID(), Label: "Friends",
		LibraryIDs: []string{"22222222-2222-4222-8222-222222222222"},
	}
	if _, err := service.Create(context.Background(), input); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("Create unknown library = %v", err)
	}
	input.LibraryIDs = nil
	created, err := service.Create(context.Background(), input)
	if err != nil || created.Code == "" || store.invite.ID == "" || len(store.invite.LibraryIDs) != 0 {
		t.Fatalf("Create default-all = %+v, %v", created, err)
	}
}

func TestAcceptCompensatesPolicyAndCommitFailures(t *testing.T) {
	tests := []struct {
		name       string
		policyErr  error
		commitErr  error
		wantDelete int
		cancel     bool
	}{
		{name: "policy", policyErr: errors.New("policy failed"), wantDelete: 1},
		{name: "commit", commitErr: errors.New("commit failed"), wantDelete: 1},
		{name: "canceled commit", commitErr: errors.New("commit failed"), wantDelete: 1, cancel: true},
		{name: "success"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
			store := &fakeStore{invite: activeInvite(now), failCommit: testCase.commitErr}
			provisioner := &fakeProvisioner{policyErr: testCase.policyErr}
			service := newService(t, store, defaultServers(), provisioner, now)
			ctx, cancel := context.WithCancel(context.Background())
			if testCase.cancel {
				cancel()
			} else {
				defer cancel()
			}
			accepted, err := service.Accept(ctx, validCode(t), "new-user", "Th1s-is-a-unique-password!")
			wantErr := testCase.policyErr != nil || testCase.commitErr != nil
			if (err != nil) != wantErr {
				t.Fatalf("Accept = %+v, %v; wantErr=%t", accepted, err, wantErr)
			}
			if provisioner.created != 1 || provisioner.policies != 1 || provisioner.deleted != testCase.wantDelete {
				t.Fatalf("calls create=%d policy=%d delete=%d", provisioner.created, provisioner.policies, provisioner.deleted)
			}
			if provisioner.deleteContextCanceled {
				t.Fatal("compensating delete inherited a canceled request context")
			}
		})
	}
}

func TestAcceptRecordsDeleteExhaustionWithoutConsumingInvite(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{invite: activeInvite(now)}
	provisioner := &fakeProvisioner{policyErr: errors.New("policy failed"), deleteErr: errors.New("delete exhausted")}
	service := newService(t, store, defaultServers(), provisioner, now)

	_, err := service.Accept(context.Background(), validCode(t), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, core.ErrInviteProvisioningPending) {
		t.Fatalf("Accept error = %v, want ErrInviteProvisioningPending", err)
	}
	if len(store.redemptions) != 0 || len(store.failures) != 1 {
		t.Fatalf("redemptions=%d failures=%d, want 0 and 1", len(store.redemptions), len(store.failures))
	}
	if provisioner.deleted != 2 {
		t.Fatalf("delete attempts = %d, want 2 bounded calls", provisioner.deleted)
	}
	failure := store.failures[0]
	if failure.InviteID != store.invite.ID || failure.MediaServerID != store.invite.MediaServerID ||
		failure.MediaUserID == "" || failure.Username != "new-user" || failure.Reason != core.InviteProvisioningCleanupFailed {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestAcceptSurfacesProvisioningFailureInsertError(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	recordErr := errors.New("record failure")
	store := &fakeStore{invite: activeInvite(now), failureErr: recordErr}
	provisioner := &fakeProvisioner{policyErr: errors.New("policy failed"), deleteErr: errors.New("delete exhausted")}
	service := newService(t, store, defaultServers(), provisioner, now)

	_, err := service.Accept(context.Background(), validCode(t), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, recordErr) || !errors.Is(err, core.ErrInviteProvisioningPending) {
		t.Fatalf("Accept error = %v, want pending and record failure", err)
	}
}

func TestAcceptDeletesUserFoundAfterAmbiguousCreate(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{invite: activeInvite(now)}
	provisioner := &fakeProvisioner{createErr: core.ErrMediaUserCreateAmbiguous}
	service := newService(t, store, defaultServers(), provisioner, now)

	_, err := service.Accept(context.Background(), validCode(t), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, core.ErrMediaUserCreateAmbiguous) {
		t.Fatalf("Accept error = %v, want ambiguous create", err)
	}
	if provisioner.deleted != 1 || len(store.failures) != 0 || len(store.redemptions) != 0 {
		t.Fatalf("delete=%d failures=%d redemptions=%d, want 1, 0, 0",
			provisioner.deleted, len(store.failures), len(store.redemptions))
	}
}

func TestPreviewUsesFakeClockForExpiry(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Minute)
	store := &fakeStore{invite: activeInvite(now)}
	store.invite.ExpiresAt = &expires
	clock := testutil.NewFakeClock(now)
	service, err := invite.NewService(store, store, defaultServers(), fakeAcquirer{&fakeProvisioner{}}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Preview(context.Background(), validCode(t)); err != nil {
		t.Fatalf("active Preview: %v", err)
	}
	clock.Advance(time.Minute)
	if _, err := service.Preview(context.Background(), validCode(t)); !errors.Is(err, core.ErrInviteUnavailable) {
		t.Fatalf("expired Preview = %v", err)
	}
}

func newService(
	t *testing.T, store *fakeStore, servers fakeServers, provisioner *fakeProvisioner, now time.Time,
) *invite.Service {
	t.Helper()
	service, err := invite.NewService(store, store, servers, fakeAcquirer{provisioner}, testutil.NewFakeClock(now))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func activeInvite(now time.Time) core.Invite {
	return core.Invite{
		ID: "44444444-4444-4444-8444-444444444444", MediaServerID: testServerID(),
		CreatedBy: testAccountID(), Label: "Friends", CreatedAt: now, UpdatedAt: now,
	}
}

func defaultServers() fakeServers {
	return fakeServers{connection: core.MediaServerConnection{Server: core.MediaServer{ID: testServerID(), Name: "Home"}}}
}

func validCode(t *testing.T) string {
	t.Helper()
	code, _, err := core.NewInviteCode()
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func testServerID() string  { return "33333333-3333-4333-8333-333333333333" }
func testAccountID() string { return "11111111-1111-4111-8111-111111111111" }
