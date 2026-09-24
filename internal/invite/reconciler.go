package invite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	defaultReconcileBatch   = 50
	defaultReconcileTimeout = 10 * time.Second
	defaultReconcileStore   = 5 * time.Second
	defaultReconcileLease   = 30 * time.Second
	defaultReconcileBase    = 5 * time.Second
	maxReconcileDelay       = 6 * time.Hour
)

var (
	errManualResolution = errors.New(core.InviteManualResolutionError)
	errProvisionerPanic = errors.New("media server reconciliation failed unexpectedly")
)

// ReconcileMetrics observes bounded-cardinality outcomes and backlog depth.
type ReconcileMetrics interface {
	ObserveInviteReconciliation(reason, outcome string)
	SetInviteProvisioningBacklog(int64)
}

// ReconcilerConfig bounds polling, claims, external work, and retries.
type ReconcilerConfig struct {
	Interval      time.Duration
	BatchSize     int
	OperationTime time.Duration
	StoreTimeout  time.Duration
	LeaseDuration time.Duration
	RetryBase     time.Duration
}

// ReconcilerDependencies contains the worker's injected boundaries.
type ReconcilerDependencies struct {
	Store        core.InviteProvisioningFailureStore
	Provisioners provisionerAcquirer
	Clock        core.Clock
	Metrics      ReconcileMetrics
	Logger       *slog.Logger
	Wait         func(context.Context, time.Duration) error
}

// Reconciler retries durable invite provisioning obligations.
type Reconciler struct {
	config ReconcilerConfig
	deps   ReconcilerDependencies
}

// NewReconciler validates and constructs an invite reconciliation worker.
func NewReconciler(config ReconcilerConfig, deps ReconcilerDependencies) (*Reconciler, error) {
	applyReconcilerDefaults(&config)
	minimumLease := config.OperationTime + 3*config.StoreTimeout
	if config.Interval < time.Second || config.BatchSize < 1 || config.BatchSize > 100 ||
		config.OperationTime <= 0 || config.StoreTimeout <= 0 || config.LeaseDuration <= minimumLease || config.RetryBase <= 0 {
		return nil, core.ErrInvalidArgument
	}
	if deps.Store == nil || deps.Provisioners == nil || deps.Clock == nil || deps.Metrics == nil || deps.Logger == nil {
		return nil, errors.New("invite reconciler: all dependencies are required")
	}
	worker := &Reconciler{config: config, deps: deps}
	if worker.deps.Wait == nil {
		worker.deps.Wait = waitForReconcile
	}
	return worker, nil
}

func applyReconcilerDefaults(config *ReconcilerConfig) {
	if config.BatchSize == 0 {
		config.BatchSize = defaultReconcileBatch
	}
	if config.OperationTime == 0 {
		config.OperationTime = defaultReconcileTimeout
	}
	if config.StoreTimeout == 0 {
		config.StoreTimeout = defaultReconcileStore
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = max(defaultReconcileLease, config.OperationTime+3*config.StoreTimeout+time.Second)
	}
	if config.RetryBase == 0 {
		config.RetryBase = defaultReconcileBase
	}
}

// Run reconciles due failures until cancellation.
func (w *Reconciler) Run(ctx context.Context) error {
	for {
		if err := w.runOnce(ctx); err != nil && ctx.Err() == nil {
			w.deps.Logger.WarnContext(ctx, "invite reconciliation pass failed", "error", err)
		}
		if ctx.Err() != nil {
			return nil //nolint:nilerr // cancellation is the clean shutdown signal.
		}
		if err := w.deps.Wait(ctx, w.config.Interval); err != nil {
			if ctx.Err() != nil {
				return nil //nolint:nilerr // cancellation is the clean shutdown signal.
			}
			return fmt.Errorf("wait for invite reconciliation: %w", err)
		}
	}
}

func (w *Reconciler) runOnce(ctx context.Context) error {
	var result error
	for range w.config.BatchSize {
		if ctx.Err() != nil {
			return errors.Join(result, ctx.Err())
		}
		failure, err := w.claim(ctx)
		if errors.Is(err, core.ErrNotFound) {
			break
		}
		if err != nil {
			result = errors.Join(result, err)
			break
		}
		if err := w.reconcile(ctx, failure); err != nil {
			result = errors.Join(result, err)
		}
	}
	w.observeBacklog(ctx)
	return result
}

func (w *Reconciler) claim(ctx context.Context) (core.InviteProvisioningFailure, error) {
	now := core.NormalizeTime(w.deps.Clock.Now())
	token, err := core.NewID()
	if err != nil {
		return core.InviteProvisioningFailure{}, fmt.Errorf("create invite reconciliation lease: %w", err)
	}
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	return w.deps.Store.ClaimInviteProvisioningFailure(storeCtx, core.InviteProvisioningLease{
		Token: token, ExpiresAt: now.Add(w.config.LeaseDuration),
	}, now)
}

func (w *Reconciler) reconcile(ctx context.Context, failure core.InviteProvisioningFailure) error {
	if !failure.MediaUserOwned ||
		(failure.Reason == core.InviteProvisioningCreateAmbiguous && failure.MediaUserID == "") {
		return w.fail(ctx, failure, errManualResolution)
	}
	redemption, err := w.runProvisionerOperation(ctx, failure)
	if err != nil {
		return w.fail(ctx, failure, err)
	}
	if failure.Reason == core.InviteProvisioningCleanupFailed {
		err = w.completeCleanup(ctx, failure)
	} else {
		err = w.completePolicy(ctx, failure, redemption)
	}
	if err != nil {
		return w.fail(ctx, failure, err)
	}
	w.deps.Metrics.ObserveInviteReconciliation(string(failure.Reason), "completed")
	return nil
}

func (w *Reconciler) runProvisionerOperation(
	ctx context.Context, failure core.InviteProvisioningFailure,
) (redemption core.InviteRedemption, retErr error) {
	opCtx, cancel := context.WithTimeout(ctx, w.config.OperationTime)
	defer cancel()
	defer func() {
		if recover() != nil {
			redemption, retErr = core.InviteRedemption{}, errProvisionerPanic
		}
	}()
	provisioner, release, err := w.deps.Provisioners.AcquireUserProvisioner(opCtx, failure.MediaServerID)
	if err != nil {
		return core.InviteRedemption{}, err
	}
	defer release()
	if failure.Reason == core.InviteProvisioningCleanupFailed {
		return core.InviteRedemption{}, provisioner.DeleteUser(opCtx, failure.MediaUserID)
	}
	return w.prepareAmbiguousPolicy(opCtx, failure, provisioner)
}

func (w *Reconciler) prepareAmbiguousPolicy(
	ctx context.Context, failure core.InviteProvisioningFailure, provisioner core.MediaUserProvisioner,
) (core.InviteRedemption, error) {
	if !failure.MediaUserOwned || failure.MediaUserID == "" {
		return core.InviteRedemption{}, errManualResolution
	}
	lookup, ok := provisioner.(core.MediaUserIDLookup)
	if !ok {
		return core.InviteRedemption{}, errManualResolution
	}
	user, found, err := lookup.FindUserByID(ctx, failure.MediaUserID)
	if err != nil {
		return core.InviteRedemption{}, err
	}
	if !found || user.ID != failure.MediaUserID || user.Name != failure.Username {
		return core.InviteRedemption{}, errManualResolution
	}
	err = provisioner.SetLibraryAccess(ctx, user.ID, failure.LibraryIDs, len(failure.LibraryIDs) == 0)
	if err != nil {
		return core.InviteRedemption{}, err
	}
	id, err := core.NewID()
	if err != nil {
		return core.InviteRedemption{}, err
	}
	now := core.NormalizeTime(w.deps.Clock.Now())
	return core.InviteRedemption{
		ID: id, InviteID: failure.InviteID, AccountID: failure.AccountID,
		MediaServerID: failure.MediaServerID, MediaUserID: user.ID,
		Username: failure.Username, RedeemedAt: now,
	}, nil
}

func (w *Reconciler) fail(ctx context.Context, failure core.InviteProvisioningFailure, cause error) error {
	now := core.NormalizeTime(w.deps.Clock.Now())
	terminal := errors.Is(cause, errManualResolution) || failure.Attempts >= core.MaxInviteProvisioningAttempts
	next := now.Add(w.retryDelay(failure.Attempts))
	safeError := safeReconciliationError(cause)
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	if err := w.deps.Store.RescheduleInviteProvisioningFailure(
		storeCtx, failure.ID, failure.LeaseToken, safeError, now, next, terminal,
	); err != nil {
		return fmt.Errorf("record invite reconciliation outcome: %w", err)
	}
	outcome := "retry"
	if terminal {
		outcome = "terminal"
	}
	w.deps.Metrics.ObserveInviteReconciliation(string(failure.Reason), outcome)
	w.deps.Logger.WarnContext(ctx, "invite provisioning reconciliation failed",
		"failure_id", failure.ID, "reason", failure.Reason, "attempts", failure.Attempts,
		"outcome", outcome, "safe_error", safeError)
	return fmt.Errorf("reconcile invite provisioning failure: %s", safeError)
}

func (w *Reconciler) retryDelay(attempt int) time.Duration {
	exponent := max(0, min(attempt-1, core.MaxInviteProvisioningAttempts-1))
	return min(w.config.RetryBase*time.Duration(1<<exponent), maxReconcileDelay)
}

func (w *Reconciler) observeBacklog(ctx context.Context) {
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	depth, err := w.deps.Store.InviteProvisioningFailureDepth(storeCtx)
	if err != nil {
		w.deps.Logger.WarnContext(ctx, "read invite provisioning backlog", "error", err)
		return
	}
	w.deps.Metrics.SetInviteProvisioningBacklog(depth)
}

func safeReconciliationError(err error) string {
	if errors.Is(err, errManualResolution) {
		return errManualResolution.Error()
	}
	if mediaErr, ok := errors.AsType[*core.MediaServerError](err); ok {
		value := "media server " + mediaErr.Operation + ": " + string(mediaErr.Kind)
		return value[:min(len(value), core.MaxInviteProvisioningErrorBytes)]
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "reconciliation operation timed out"
	}
	return "media server reconciliation failed"
}

func (w *Reconciler) completeCleanup(ctx context.Context, failure core.InviteProvisioningFailure) error {
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	return w.deps.Store.CompleteInviteProvisioningCleanup(storeCtx, failure.ID, failure.LeaseToken)
}

func (w *Reconciler) completePolicy(
	ctx context.Context, failure core.InviteProvisioningFailure, redemption core.InviteRedemption,
) error {
	storeCtx, cancel := w.storeContext(ctx)
	defer cancel()
	now := core.NormalizeTime(w.deps.Clock.Now())
	return w.deps.Store.CompleteInviteProvisioningPolicy(storeCtx, failure, redemption, now)
}

func (w *Reconciler) storeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, w.config.StoreTimeout)
}

func waitForReconcile(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
