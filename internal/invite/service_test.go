package invite_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/invite"
	"github.com/BonzTM/bloom/internal/testutil"
)

type fakeStore struct {
	invite                 core.Invite
	createHash             [sha256.Size]byte
	createErr              error
	redeemErr              error
	failCommit             error
	redemptions            []core.InviteRedemption
	failures               []core.InviteProvisioningFailure
	transactionFailureErr  error
	failureErr             error
	ambiguousFailureCommit error
	recordAttempts         int
	lookupHash             *[sha256.Size]byte
	blocked                bool
	lookups                int
	redeems                int
	dismissedAt            time.Time
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
) (bool, error) {
	s.redeems++
	if s.redeemErr != nil {
		return false, s.redeemErr
	}
	redemption, err := redeem(ctx, s.invite)
	if err != nil {
		if provisioningErr, ok := errors.AsType[*core.InviteProvisioningError](err); ok && provisioningErr != nil {
			if s.transactionFailureErr != nil {
				return false, errors.Join(provisioningErr.Err, s.transactionFailureErr)
			}
			s.appendFailureIfAbsent(provisioningErr.Failure)
			if s.ambiguousFailureCommit != nil {
				return false, errors.Join(provisioningErr.Err, s.ambiguousFailureCommit)
			}
			return false, err
		}
		return false, err
	}
	s.redemptions = append(s.redemptions, redemption)
	return redemption.AccountID != "", s.failCommit
}

func (s *fakeStore) RecordInviteProvisioningFailure(
	_ context.Context, failure core.InviteProvisioningFailure,
) error {
	s.recordAttempts++
	if s.failureErr != nil {
		return s.failureErr
	}
	s.appendFailureIfAbsent(failure)
	return nil
}

func (s *fakeStore) appendFailureIfAbsent(failure core.InviteProvisioningFailure) {
	for _, existing := range s.failures {
		if existing.ID == failure.ID {
			return
		}
	}
	s.failures = append(s.failures, failure)
}

func (s *fakeStore) GetInvite(_ context.Context, _ string) (core.Invite, error) { return s.invite, nil }

func (s *fakeStore) GetInviteByCodeHash(_ context.Context, hash [sha256.Size]byte) (core.InviteCodeLookup, error) {
	s.lookups++
	stored := hash
	if s.lookupHash != nil {
		stored = *s.lookupHash
	}
	return core.InviteCodeLookup{Invite: s.invite, CodeHash: stored, Blocked: s.blocked}, nil
}

func (s *fakeStore) ListInvites(context.Context, *core.InviteCursor, int) ([]core.Invite, error) {
	return []core.Invite{s.invite}, nil
}

func (*fakeStore) ClaimInviteProvisioningFailure(context.Context, core.InviteProvisioningLease, time.Time) (core.InviteProvisioningFailure, error) {
	return core.InviteProvisioningFailure{}, core.ErrNotFound
}

func (*fakeStore) CompleteInviteProvisioningCleanup(context.Context, string, string) error {
	return nil
}

func (*fakeStore) CompleteInviteProvisioningPolicy(context.Context, core.InviteProvisioningFailure, core.InviteRedemption, time.Time) error {
	return nil
}

func (*fakeStore) RescheduleInviteProvisioningFailure(context.Context, string, string, string, time.Time, time.Time, bool) error {
	return nil
}

func (*fakeStore) ListInviteProvisioningFailures(context.Context, *core.InviteProvisioningFailureCursor, int) ([]core.InviteProvisioningFailure, error) {
	return nil, nil
}

func (s *fakeStore) DismissInviteProvisioningFailure(_ context.Context, _ string, at time.Time) error {
	s.dismissedAt = at
	return nil
}
func (*fakeStore) InviteProvisioningFailureDepth(context.Context) (int64, error) { return 0, nil }

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

func (s fakeServers) ListInviteServers(context.Context, string, int) ([]core.InviteServer, error) {
	return []core.InviteServer{{ID: s.connection.Server.ID, Name: s.connection.Server.Name}}, nil
}

type fakeProvisioner struct {
	createErr, policyErr, deleteErr error
	created, policies, deleted      int
	lastPassword                    string
	deleteContextCanceled           bool
	panicCreate, panicPolicy        bool
	panicDelete, live               bool
}

func (p *fakeProvisioner) CreateUser(_ context.Context, name, password string) (core.MediaUser, error) {
	p.created++
	p.lastPassword = password
	p.live = true
	if p.panicCreate {
		panic("create panic with " + password)
	}
	return core.MediaUser{
		ID: "55555555-5555-4555-8555-555555555555", Name: name, Created: p.createErr == nil,
	}, p.createErr
}

func (p *fakeProvisioner) SetLibraryAccess(context.Context, string, []string, bool) error {
	p.policies++
	if p.panicPolicy {
		panic("policy panic with " + p.lastPassword)
	}
	return p.policyErr
}

func (p *fakeProvisioner) DeleteUser(ctx context.Context, _ string) error {
	p.deleted++
	p.deleteContextCanceled = ctx.Err() != nil
	if p.panicDelete {
		panic("delete panic with " + p.lastPassword)
	}
	if p.deleteErr == nil {
		p.live = false
	}
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

func TestDismissProvisioningFailureUsesNormalizedClock(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 123456789, time.FixedZone("west", -4*60*60))
	store := &fakeStore{}
	service := newService(t, store, defaultServers(), &fakeProvisioner{}, now)
	if err := service.DismissProvisioningFailure(t.Context(), "55555555-5555-4555-8555-555555555555"); err != nil {
		t.Fatalf("DismissProvisioningFailure: %v", err)
	}
	if want := core.NormalizeTime(now); !store.dismissedAt.Equal(want) || store.dismissedAt.Location() != time.UTC {
		t.Fatalf("dismissed at = %v, want %v", store.dismissedAt, want)
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
			accepted, err := service.Accept(ctx, "", validCode(t), "new-user", "Th1s-is-a-unique-password!")
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

	_, err := service.Accept(context.Background(), "", validCode(t), "new-user", "Th1s-is-a-unique-password!")
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
		failure.MediaUserID == "" || !failure.MediaUserOwned || failure.Username != "new-user" ||
		failure.Reason != core.InviteProvisioningCleanupFailed {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestAcceptContainsProvisionerPanicsAndTracksCreatedUser(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		provisioner fakeProvisioner
		wantReason  core.InviteProvisioningFailureReason
	}{
		{name: "create", provisioner: fakeProvisioner{panicCreate: true}, wantReason: core.InviteProvisioningCreateAmbiguous},
		{name: "policy", provisioner: fakeProvisioner{panicPolicy: true}},
		{name: "cleanup", provisioner: fakeProvisioner{policyErr: errors.New("policy failed"), panicDelete: true}, wantReason: core.InviteProvisioningCleanupFailed},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := &fakeStore{invite: activeInvite(now)}
			provisioner := testCase.provisioner
			service := newService(t, store, defaultServers(), &provisioner, now)
			_, err := service.Accept(t.Context(), "", validCode(t), "new-user", "Th1s-is-a-unique-password!")
			if err == nil || (provisioner.live && len(store.failures) != 1) {
				t.Fatalf("Accept error=%v live=%t failures=%d", err, provisioner.live, len(store.failures))
			}
			if testCase.wantReason != "" && store.failures[0].Reason != testCase.wantReason {
				t.Fatalf("failure reason = %q, want %q", store.failures[0].Reason, testCase.wantReason)
			}
		})
	}
}

func TestAcceptPersistsFailureAfterTransactionalRollback(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	transactionErr := errors.New("transaction failed")
	store := &fakeStore{invite: activeInvite(now), transactionFailureErr: transactionErr}
	provisioner := &fakeProvisioner{policyErr: errors.New("policy failed"), deleteErr: errors.New("delete exhausted")}
	service := newService(t, store, defaultServers(), provisioner, now)

	_, err := service.Accept(context.Background(), "", validCode(t), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, transactionErr) || !errors.Is(err, core.ErrInviteProvisioningPending) {
		t.Fatalf("Accept error = %v, want pending and transaction failure", err)
	}
	if len(store.failures) != 1 || store.recordAttempts != 1 {
		t.Fatalf("failures=%d fallback attempts=%d, want 1 and 1", len(store.failures), store.recordAttempts)
	}
}

func TestAcceptDoesNotFakeFailedFallbackPersistence(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	transactionErr, fallbackErr := errors.New("transaction failed"), errors.New("fallback failed")
	store := &fakeStore{
		invite: activeInvite(now), transactionFailureErr: transactionErr, failureErr: fallbackErr,
	}
	provisioner := &fakeProvisioner{policyErr: errors.New("policy failed"), deleteErr: errors.New("delete exhausted")}
	service := newService(t, store, defaultServers(), provisioner, now)

	_, err := service.Accept(t.Context(), "", validCode(t), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, fallbackErr) || !errors.Is(err, core.ErrInviteProvisioningPending) {
		t.Fatalf("Accept error = %v, want pending and fallback failure", err)
	}
	if len(store.failures) != 0 || store.recordAttempts != 1 {
		t.Fatalf("failures=%d fallback attempts=%d, want 0 and 1", len(store.failures), store.recordAttempts)
	}
}

func TestAcceptRetriesAmbiguousFailureCommitByOriginalID(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	ambiguous := errors.New("commit outcome unknown")
	store := &fakeStore{invite: activeInvite(now), ambiguousFailureCommit: ambiguous}
	provisioner := &fakeProvisioner{policyErr: errors.New("policy failed"), deleteErr: errors.New("delete exhausted")}
	service := newService(t, store, defaultServers(), provisioner, now)

	_, err := service.Accept(t.Context(), "", validCode(t), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, ambiguous) || !errors.Is(err, core.ErrInviteProvisioningPending) {
		t.Fatalf("Accept error = %v, want pending and ambiguous commit", err)
	}
	if len(store.failures) != 1 || store.recordAttempts != 1 {
		t.Fatalf("failures=%d fallback attempts=%d, want 1 and 1", len(store.failures), store.recordAttempts)
	}
}

func TestAcceptLeavesUnownedUserFoundAfterAmbiguousCreate(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{invite: activeInvite(now)}
	provisioner := &fakeProvisioner{createErr: core.ErrMediaUserCreateAmbiguous}
	service := newService(t, store, defaultServers(), provisioner, now)

	_, err := service.Accept(context.Background(), "", validCode(t), "new-user", "Th1s-is-a-unique-password!")
	if !errors.Is(err, core.ErrMediaUserCreateAmbiguous) {
		t.Fatalf("Accept error = %v, want ambiguous create", err)
	}
	if provisioner.deleted != 0 || len(store.failures) != 1 || len(store.redemptions) != 0 {
		t.Fatalf("delete=%d failures=%d redemptions=%d, want 0, 1, 0",
			provisioner.deleted, len(store.failures), len(store.redemptions))
	}
	failure := store.failures[0]
	if failure.MediaUserOwned || failure.MediaUserID == "" || !failure.Terminal ||
		failure.Reason != core.InviteProvisioningCreateAmbiguous ||
		failure.LastError != core.InviteManualResolutionError {
		t.Fatalf("failure = %+v", failure)
	}
}

func TestPreviewUsesFakeClockForExpiry(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	expires := now.Add(time.Minute)
	store := &fakeStore{invite: activeInvite(now)}
	store.invite.ExpiresAt = &expires
	clock := testutil.NewFakeClock(now)
	service, err := invite.NewService(store, store, defaultServers(), fakeAcquirer{&fakeProvisioner{}}, clock, slog.New(slog.DiscardHandler))
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

func TestUnusableInviteLookupsHaveIdenticalWork(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	code := validCode(t)
	unknownHash := sha256.Sum256([]byte("unknown"))
	one := 1
	revoked := now.Add(-time.Minute)
	expired := now.Add(-time.Second)
	tests := []struct {
		name   string
		code   string
		mutate func(*fakeStore)
	}{
		{name: "unknown", mutate: func(store *fakeStore) { store.lookupHash = &unknownHash }},
		{name: "expired", mutate: func(store *fakeStore) { store.invite.ExpiresAt = &expired }},
		{name: "exhausted", mutate: func(store *fakeStore) { store.invite.MaxUses, store.invite.UseCount = &one, 1 }},
		{name: "revoked", mutate: func(store *fakeStore) { store.invite.RevokedAt = &revoked }},
		{name: "malformed", code: "not-a-code", mutate: func(*fakeStore) {}},
		{name: "non-canonical", code: "aaaaaaaaaaaaaaaaaaaaaaaaaa", mutate: func(*fakeStore) {}},
	}
	for _, operation := range []string{"preview", "accept"} {
		var wantError string
		for _, testCase := range tests {
			t.Run(operation+"/"+testCase.name, func(t *testing.T) {
				store := &fakeStore{invite: activeInvite(now)}
				testCase.mutate(store)
				service := newService(t, store, defaultServers(), &fakeProvisioner{}, now)
				lookupCode := code
				if testCase.code != "" {
					lookupCode = testCase.code
				}
				var err error
				if operation == "preview" {
					_, err = service.Preview(t.Context(), lookupCode)
				} else {
					_, err = service.Accept(t.Context(), "", lookupCode, "new-user", "Th1s-is-a-unique-password!")
				}
				if !errors.Is(err, core.ErrInviteUnavailable) || store.lookups != 1 || store.redeems != 0 {
					t.Fatalf("error=%v lookups=%d redeems=%d", err, store.lookups, store.redeems)
				}
				if wantError == "" {
					wantError = err.Error()
				} else if err.Error() != wantError {
					t.Fatalf("error body %q, want %q", err.Error(), wantError)
				}
			})
		}
	}
}

func newService(
	t *testing.T, store *fakeStore, servers fakeServers, provisioner *fakeProvisioner, now time.Time,
) *invite.Service {
	t.Helper()
	service, err := invite.NewService(store, store, servers, fakeAcquirer{provisioner}, testutil.NewFakeClock(now), slog.New(slog.DiscardHandler))
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
