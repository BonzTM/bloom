package invite

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/BonzTM/bloom/internal/core"
	"github.com/BonzTM/bloom/internal/testutil"
)

type reconcilerStore struct {
	failure     core.InviteProvisioningFailure
	claimed     bool
	cleanupDone bool
	policyDone  bool
	rescheduled bool
	terminal    bool
	redemption  core.InviteRedemption
	lastError   string
	backlog     int64
	blockPolicy bool
	settleErr   error
	settleToken string
}

func (s *reconcilerStore) ClaimInviteProvisioningFailure(context.Context, core.InviteProvisioningLease, time.Time) (core.InviteProvisioningFailure, error) {
	if s.claimed {
		return core.InviteProvisioningFailure{}, core.ErrNotFound
	}
	s.claimed = true
	return s.failure, nil
}

func (s *reconcilerStore) CompleteInviteProvisioningCleanup(context.Context, string, string) error {
	s.cleanupDone = true
	return nil
}

func (s *reconcilerStore) CompleteInviteProvisioningPolicy(ctx context.Context, _ core.InviteProvisioningFailure, redemption core.InviteRedemption, _ time.Time) error {
	if s.blockPolicy {
		<-ctx.Done()
		s.settleErr = ctx.Err()
		return ctx.Err()
	}
	s.policyDone, s.redemption = true, redemption
	return nil
}

func (s *reconcilerStore) RescheduleInviteProvisioningFailure(_ context.Context, _, token, safeError string, _, _ time.Time, terminal bool) error {
	s.rescheduled, s.terminal, s.lastError = true, terminal, safeError
	s.settleToken = token
	return nil
}

func (*reconcilerStore) ListInviteProvisioningFailures(context.Context, *core.InviteProvisioningFailureCursor, int) ([]core.InviteProvisioningFailure, error) {
	return nil, nil
}

func (*reconcilerStore) DismissInviteProvisioningFailure(context.Context, string, time.Time) error {
	return nil
}

func (s *reconcilerStore) InviteProvisioningFailureDepth(context.Context) (int64, error) {
	return s.backlog, nil
}

type reconcilerProvisioner struct {
	user        core.MediaUser
	found       bool
	deleteErr   error
	policyErr   error
	deleted     int
	policies    int
	idLookups   int
	nameLookups int
	releases    int
	panicDelete bool
	panicLookup bool
	panicPolicy bool
	opContext   context.Context
}

func (*reconcilerProvisioner) CreateUser(context.Context, string, string) (core.MediaUser, error) {
	return core.MediaUser{}, errors.New("unexpected create")
}

func (p *reconcilerProvisioner) SetLibraryAccess(ctx context.Context, _ string, _ []string, _ bool) error {
	p.policies++
	p.opContext = ctx
	if p.panicPolicy {
		panic("policy-panic-sensitive-value")
	}
	return p.policyErr
}

func (p *reconcilerProvisioner) DeleteUser(ctx context.Context, _ string) error {
	p.deleted++
	p.opContext = ctx
	if p.panicDelete {
		panic("cleanup-panic-sensitive-value")
	}
	return p.deleteErr
}

func (p *reconcilerProvisioner) FindUserByName(context.Context, string) (core.MediaUser, bool, error) {
	p.nameLookups++
	return p.user, p.found, nil
}

func (p *reconcilerProvisioner) FindUserByID(ctx context.Context, _ string) (core.MediaUser, bool, error) {
	p.idLookups++
	p.opContext = ctx
	if p.panicLookup {
		panic("lookup-panic-sensitive-value")
	}
	return p.user, p.found, nil
}

type reconcilerAcquirer struct{ provisioner *reconcilerProvisioner }

func (a reconcilerAcquirer) AcquireUserProvisioner(context.Context, string) (core.MediaUserProvisioner, func(), error) {
	return a.provisioner, func() { a.provisioner.releases++ }, nil
}

type reconcilerMetrics struct {
	reason, outcome string
	depth           int64
}

func (m *reconcilerMetrics) ObserveInviteReconciliation(reason, outcome string) {
	m.reason, m.outcome = reason, outcome
}
func (m *reconcilerMetrics) SetInviteProvisioningBacklog(depth int64) { m.depth = depth }

func TestReconcilerCompletesCleanupAndAmbiguousPolicy(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name   string
		reason core.InviteProvisioningFailureReason
	}{
		{name: "cleanup", reason: core.InviteProvisioningCleanupFailed},
		{name: "ambiguous policy", reason: core.InviteProvisioningCreateAmbiguous},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &reconcilerStore{failure: reconciliationFixture(now, testCase.reason), backlog: 3}
			provisioner := &reconcilerProvisioner{
				user: core.MediaUser{ID: "66666666-6666-4666-8666-666666666666", Name: "new-user"}, found: true,
			}
			metrics := &reconcilerMetrics{}
			worker := newTestReconciler(t, now, store, provisioner, metrics)
			if err := worker.runOnce(t.Context()); err != nil {
				t.Fatalf("runOnce: %v", err)
			}
			if metrics.outcome != "completed" || metrics.depth != 3 {
				t.Fatalf("metrics = %+v", metrics)
			}
			if testCase.reason == core.InviteProvisioningCleanupFailed && (!store.cleanupDone || provisioner.deleted != 1) {
				t.Fatalf("cleanup done=%v deletes=%d", store.cleanupDone, provisioner.deleted)
			}
			if testCase.reason == core.InviteProvisioningCreateAmbiguous && (!store.policyDone || provisioner.policies != 1 || store.redemption.MediaUserID == "") {
				t.Fatalf("policy done=%v policies=%d redemption=%+v", store.policyDone, provisioner.policies, store.redemption)
			}
			if provisioner.nameLookups != 0 {
				t.Fatalf("name lookups = %d, want 0", provisioner.nameLookups)
			}
		})
	}
}

func TestReconcilerTerminatesUnverifiableAmbiguousIdentity(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		userID    string
		user      core.MediaUser
		found     bool
		owned     bool
		idLookups int
	}{
		{name: "unowned stable id", userID: "66666666-6666-4666-8666-666666666666"},
		{name: "missing stable id", owned: true},
		{name: "stable id deleted", userID: "66666666-6666-4666-8666-666666666666", owned: true, idLookups: 1},
		{
			name: "stable id renamed", userID: "66666666-6666-4666-8666-666666666666",
			user:  core.MediaUser{ID: "66666666-6666-4666-8666-666666666666", Name: "replacement"},
			found: true, owned: true, idLookups: 1,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			failure := reconciliationFixture(now, core.InviteProvisioningCreateAmbiguous)
			failure.MediaUserID, failure.MediaUserOwned = testCase.userID, testCase.owned
			store := &reconcilerStore{failure: failure}
			provisioner := &reconcilerProvisioner{user: testCase.user, found: testCase.found}
			worker := newTestReconciler(t, now, store, provisioner, &reconcilerMetrics{})
			if err := worker.runOnce(t.Context()); err == nil {
				t.Fatal("runOnce succeeded")
			}
			if !store.rescheduled || !store.terminal || store.lastError != errManualResolution.Error() ||
				provisioner.deleted != 0 || provisioner.policies != 0 || provisioner.nameLookups != 0 ||
				provisioner.idLookups != testCase.idLookups {
				t.Fatalf("store=%+v provisioner=%+v", store, provisioner)
			}
		})
	}
}

func TestReconcilerBoundsSettlementAndReleasesLease(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	store := &reconcilerStore{
		failure: reconciliationFixture(now, core.InviteProvisioningCreateAmbiguous), blockPolicy: true,
	}
	provisioner := &reconcilerProvisioner{
		user: core.MediaUser{ID: store.failure.MediaUserID, Name: store.failure.Username}, found: true,
	}
	worker := newTestReconcilerWithConfig(t, now, store, provisioner, &reconcilerMetrics{}, ReconcilerConfig{
		Interval: time.Second, StoreTimeout: 20 * time.Millisecond,
	})
	started := time.Now()
	if err := worker.runOnce(t.Context()); err == nil {
		t.Fatal("runOnce succeeded")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("runOnce took %s, want bounded settlement", elapsed)
	}
	if !errors.Is(store.settleErr, context.DeadlineExceeded) || !store.rescheduled ||
		store.settleToken != store.failure.LeaseToken {
		t.Fatalf("settlement error=%v rescheduled=%t token=%q", store.settleErr, store.rescheduled, store.settleToken)
	}
}

func TestReconcilerCapsAttemptsAndStoresOnlySafeErrors(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	failure := reconciliationFixture(now, core.InviteProvisioningCleanupFailed)
	failure.Attempts = core.MaxInviteProvisioningAttempts
	store := &reconcilerStore{failure: failure}
	secret := "sensitive-upstream-detail"
	provisioner := &reconcilerProvisioner{deleteErr: errors.New(secret)}
	metrics := &reconcilerMetrics{}
	worker := newTestReconciler(t, now, store, provisioner, metrics)
	if err := worker.runOnce(t.Context()); err == nil {
		t.Fatal("runOnce succeeded")
	}
	if !store.rescheduled || !store.terminal || store.lastError != "media server reconciliation failed" || metrics.outcome != "terminal" {
		t.Fatalf("store=%+v metrics=%+v", store, metrics)
	}
}

func TestReconcilerContainsProvisionerPanicsAndContinues(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		reason   core.InviteProvisioningFailureReason
		terminal bool
		arm      func(*reconcilerProvisioner, bool)
	}{
		{name: "cleanup", reason: core.InviteProvisioningCleanupFailed, arm: setCleanupPanic},
		{name: "identity lookup", reason: core.InviteProvisioningCreateAmbiguous, terminal: true, arm: setLookupPanic},
		{name: "policy completion", reason: core.InviteProvisioningCreateAmbiguous, terminal: true, arm: setPolicyPanic},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			testReconcilerProvisionerPanic(t, now, testCase.reason, testCase.terminal, testCase.arm)
		})
	}
}

func testReconcilerProvisionerPanic(
	t *testing.T, now time.Time, reason core.InviteProvisioningFailureReason, terminal bool,
	arm func(*reconcilerProvisioner, bool),
) {
	t.Helper()
	failure := reconciliationFixture(now, reason)
	if terminal {
		failure.Attempts = core.MaxInviteProvisioningAttempts
	}
	store := &reconcilerStore{failure: failure}
	provisioner := &reconcilerProvisioner{user: core.MediaUser{ID: failure.MediaUserID, Name: failure.Username}, found: true}
	arm(provisioner, true)
	metrics, logs := &reconcilerMetrics{}, &bytes.Buffer{}
	worker := newTestReconcilerWithLogger(t, now, store, provisioner, metrics, logs)
	if err := worker.runOnce(t.Context()); err == nil {
		t.Fatal("runOnce succeeded after provisioner panic")
	}
	assertContainedProvisionerPanic(t, store, provisioner, metrics, logs.String(), terminal)
	arm(provisioner, false)
	store.claimed, store.rescheduled, store.terminal = false, false, false
	store.failure.Attempts = 1
	if err := worker.runOnce(t.Context()); err != nil {
		t.Fatalf("worker did not continue after panic: %v", err)
	}
}

func assertContainedProvisionerPanic(
	t *testing.T, store *reconcilerStore, provisioner *reconcilerProvisioner,
	metrics *reconcilerMetrics, logs string, terminal bool,
) {
	t.Helper()
	if !store.rescheduled || store.terminal != terminal || store.lastError != "media server reconciliation failed" {
		t.Fatalf("rescheduled=%t terminal=%t error=%q", store.rescheduled, store.terminal, store.lastError)
	}
	var contextErr error
	if provisioner.opContext != nil {
		contextErr = provisioner.opContext.Err()
	}
	if provisioner.releases != 1 || !errors.Is(contextErr, context.Canceled) {
		t.Fatalf("releases=%d operation context error=%v", provisioner.releases, contextErr)
	}
	wantOutcome := "retry"
	if terminal {
		wantOutcome = "terminal"
	}
	if metrics.outcome != wantOutcome || strings.Contains(logs, "sensitive-value") {
		t.Fatalf("outcome=%q logs=%q", metrics.outcome, logs)
	}
}

func setCleanupPanic(provisioner *reconcilerProvisioner, enabled bool) {
	provisioner.panicDelete = enabled
}

func setLookupPanic(provisioner *reconcilerProvisioner, enabled bool) {
	provisioner.panicLookup = enabled
}

func setPolicyPanic(provisioner *reconcilerProvisioner, enabled bool) {
	provisioner.panicPolicy = enabled
}

func newTestReconciler(
	t *testing.T, now time.Time, store *reconcilerStore, provisioner *reconcilerProvisioner,
	metrics *reconcilerMetrics,
) *Reconciler {
	t.Helper()
	return newTestReconcilerWithConfig(t, now, store, provisioner, metrics, ReconcilerConfig{Interval: time.Second})
}

func newTestReconcilerWithConfig(
	t *testing.T, now time.Time, store *reconcilerStore, provisioner *reconcilerProvisioner,
	metrics *reconcilerMetrics, config ReconcilerConfig,
) *Reconciler {
	t.Helper()
	worker, err := NewReconciler(config, ReconcilerDependencies{
		Store: store, Provisioners: reconcilerAcquirer{provisioner}, Clock: testutil.NewFakeClock(now),
		Metrics: metrics, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func newTestReconcilerWithLogger(
	t *testing.T, now time.Time, store *reconcilerStore, provisioner *reconcilerProvisioner,
	metrics *reconcilerMetrics, logs *bytes.Buffer,
) *Reconciler {
	t.Helper()
	worker, err := NewReconciler(ReconcilerConfig{Interval: time.Second}, ReconcilerDependencies{
		Store: store, Provisioners: reconcilerAcquirer{provisioner}, Clock: testutil.NewFakeClock(now),
		Metrics: metrics, Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func reconciliationFixture(at time.Time, reason core.InviteProvisioningFailureReason) core.InviteProvisioningFailure {
	return core.InviteProvisioningFailure{
		ID: "55555555-5555-4555-8555-555555555555", InviteID: "44444444-4444-4444-8444-444444444444",
		MediaServerID: "33333333-3333-4333-8333-333333333333", MediaUserID: "66666666-6666-4666-8666-666666666666",
		MediaUserOwned: true,
		AccountID:      "11111111-1111-4111-8111-111111111111", Username: "new-user", Reason: reason,
		Attempts: 1, NextAttemptAt: at, LeaseToken: "77777777-7777-4777-8777-777777777777",
		CreatedAt: at, UpdatedAt: at,
	}
}
