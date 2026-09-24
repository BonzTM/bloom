// Package invite coordinates invite validation, persistence, and media-user provisioning.
package invite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/BonzTM/bloom/internal/core"
)

const (
	compensationTimeout       = 5 * time.Second
	invalidInviteCodeHashSeed = "bloom invalid invite code fixed-work digest"
)

var (
	errCreateUserPanic = errors.New("media user creation failed unexpectedly")
	errSetAccessPanic  = errors.New("media user library access failed unexpectedly")
	errDeleteUserPanic = errors.New("media user cleanup failed unexpectedly")
)

type mediaServers interface {
	Get(ctx context.Context, id string) (core.MediaServerConnection, error)
	Libraries(ctx context.Context, id string) ([]core.Library, error)
	ListInviteServers(ctx context.Context, afterNameKey string, pageSize int) ([]core.InviteServer, error)
}

type provisionerAcquirer interface {
	AcquireUserProvisioner(ctx context.Context, id string) (core.MediaUserProvisioner, func(), error)
}

// Service owns the invite use cases.
type Service struct {
	reader       core.InviteReader
	store        core.InviteStore
	failures     core.InviteProvisioningFailureStore
	servers      mediaServers
	provisioners provisionerAcquirer
	clock        core.Clock
	logger       *slog.Logger
}

// CreateInput is the validated intent for one invite.
type CreateInput struct {
	MediaServerID string
	CreatedBy     string
	Label         string
	ExpiresAt     *time.Time
	MaxUses       *int
	LibraryIDs    []string
}

// Created carries the only copy of the plaintext invite code.
type Created struct {
	Invite core.Invite
	Code   string
}

// Preview is the safe public metadata for an acceptable invite.
type Preview struct {
	Invite          core.Invite
	MediaServerName string
}

// Accepted is the safe result returned after provisioning.
type Accepted struct {
	InviteID        string
	MediaServerName string
	Username        string
}

// LibrarySelectionError reports a library that is not present on the target server.
type LibrarySelectionError struct{}

func (*LibrarySelectionError) Error() string { return "library_ids contains an unknown library" }
func (*LibrarySelectionError) Unwrap() error { return core.ErrInvalidArgument }

// NewService constructs an invite application service.
func NewService(
	reader core.InviteReader, store core.InviteStore, servers mediaServers,
	provisioners provisionerAcquirer, clock core.Clock, logger *slog.Logger,
) (*Service, error) {
	if reader == nil || store == nil || servers == nil || provisioners == nil || clock == nil || logger == nil {
		return nil, errors.New("invite service: all dependencies are required")
	}
	failures, ok := store.(core.InviteProvisioningFailureStore)
	if !ok {
		return nil, errors.New("invite service: provisioning failure store is required")
	}
	return &Service{
		reader: reader, store: store, failures: failures, servers: servers,
		provisioners: provisioners, clock: clock, logger: logger,
	}, nil
}

// Create validates server libraries and persists a freshly generated code hash.
func (s *Service) Create(ctx context.Context, input CreateInput) (Created, error) {
	now := core.NormalizeTime(s.clock.Now())
	invite, err := newInvite(input, now)
	if err != nil {
		return Created{}, err
	}
	if _, getErr := s.servers.Get(ctx, invite.MediaServerID); getErr != nil {
		return Created{}, fmt.Errorf("get invite media server: %w", getErr)
	}
	libraries, err := s.servers.Libraries(ctx, invite.MediaServerID)
	if err != nil {
		return Created{}, fmt.Errorf("list invite media libraries: %w", err)
	}
	if !librariesContain(libraries, invite.LibraryIDs) {
		return Created{}, &LibrarySelectionError{}
	}
	code, hash, err := core.NewInviteCode()
	if err != nil {
		return Created{}, err
	}
	if err := s.store.CreateInvite(ctx, invite, hash); err != nil {
		return Created{}, fmt.Errorf("create invite: %w", err)
	}
	return Created{Invite: invite, Code: code}, nil
}

func newInvite(input CreateInput, now time.Time) (core.Invite, error) {
	id, err := core.NewID()
	if err != nil {
		return core.Invite{}, err
	}
	invite := core.Invite{
		ID: id, MediaServerID: input.MediaServerID, CreatedBy: input.CreatedBy,
		Label: input.Label, ExpiresAt: normalizeOptionalTime(input.ExpiresAt), MaxUses: input.MaxUses,
		LibraryIDs: slices.Clone(input.LibraryIDs), CreatedAt: now, UpdatedAt: now,
	}
	if err := core.ValidateInvite(invite, now); err != nil {
		return core.Invite{}, err
	}
	return invite, nil
}

func normalizeOptionalTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := core.NormalizeTime(*value)
	return &normalized
}

func librariesContain(available []core.Library, selected []string) bool {
	if len(selected) == 0 {
		return true
	}
	known := make(map[string]struct{}, len(available))
	for _, library := range available {
		known[library.ID] = struct{}{}
	}
	for _, id := range selected {
		if _, exists := known[id]; !exists {
			return false
		}
	}
	return true
}

// List returns one newest-first page.
func (s *Service) List(ctx context.Context, after *core.InviteCursor, pageSize int) ([]core.Invite, error) {
	invites, err := s.reader.ListInvites(ctx, after, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	return invites, nil
}

// ListServers returns the least-privilege server page used by invite creation.
func (s *Service) ListServers(ctx context.Context, afterNameKey string, pageSize int) ([]core.InviteServer, error) {
	servers, err := s.servers.ListInviteServers(ctx, afterNameKey, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list invite servers: %w", err)
	}
	return servers, nil
}

// Get returns one invite by identifier.
func (s *Service) Get(ctx context.Context, id string) (core.Invite, error) {
	invite, err := s.reader.GetInvite(ctx, id)
	if err != nil {
		return core.Invite{}, fmt.Errorf("get invite: %w", err)
	}
	return invite, nil
}

// ListProvisioningFailures returns one safe newest-first administrator page.
func (s *Service) ListProvisioningFailures(
	ctx context.Context, after *core.InviteProvisioningFailureCursor, pageSize int,
) ([]core.InviteProvisioningFailure, error) {
	values, err := s.failures.ListInviteProvisioningFailures(ctx, after, pageSize)
	if err != nil {
		return nil, fmt.Errorf("list invite provisioning failures: %w", err)
	}
	return values, nil
}

// DismissProvisioningFailure removes one administrator-accepted obligation.
func (s *Service) DismissProvisioningFailure(ctx context.Context, id string) error {
	now := core.NormalizeTime(s.clock.Now())
	if err := s.failures.DismissInviteProvisioningFailure(ctx, id, now); err != nil {
		return fmt.Errorf("dismiss invite provisioning failure: %w", err)
	}
	return nil
}

// Revoke makes an invite permanently unavailable.
func (s *Service) Revoke(ctx context.Context, id string) (core.Invite, error) {
	invite, err := s.store.RevokeInvite(ctx, id, core.NormalizeTime(s.clock.Now()))
	if err != nil {
		return core.Invite{}, fmt.Errorf("revoke invite: %w", err)
	}
	return invite, nil
}

// Preview returns public metadata only while the invite remains acceptable.
func (s *Service) Preview(ctx context.Context, code string) (Preview, error) {
	hash, valid := inviteLookupHash(code)
	return s.previewByHash(ctx, hash, valid)
}

func (s *Service) previewByHash(ctx context.Context, hash [sha256.Size]byte, parseValid bool) (Preview, error) {
	lookup, err := s.reader.GetInviteByCodeHash(ctx, hash)
	matches := lookup.Matches(hash)
	if !parseValid || !matches {
		return Preview{}, core.ErrInviteUnavailable
	}
	if err != nil || lookup.Blocked || lookup.Invite.Status(s.clock.Now()) != core.InviteActive {
		return Preview{}, opaqueInviteError(err)
	}
	invite := lookup.Invite
	server, err := s.servers.Get(ctx, invite.MediaServerID)
	if err != nil {
		return Preview{}, fmt.Errorf("get invite media server: %w", err)
	}
	return Preview{Invite: invite, MediaServerName: server.Server.Name}, nil
}

func inviteLookupHash(code string) ([sha256.Size]byte, bool) {
	hash, err := core.ParseInviteCode(code)
	if err != nil {
		return sha256.Sum256([]byte(invalidInviteCodeHashSeed)), false
	}
	return hash, true
}

func opaqueInviteError(err error) error {
	if err == nil || errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrInviteUnavailable) {
		return core.ErrInviteUnavailable
	}
	return fmt.Errorf("lookup invite: %w", err)
}

// Accept provisions one media user and records the use under the store's lock.
func (s *Service) Accept(ctx context.Context, accountID, code, username, password string) (Accepted, error) {
	if accountID != "" && !core.ValidID(accountID) {
		return Accepted{}, core.ErrInvalidArgument
	}
	if err := validateAcceptance(username, password); err != nil {
		return Accepted{}, err
	}
	preview, hash, err := s.acceptancePreview(ctx, code)
	if err != nil {
		return Accepted{}, err
	}
	provisioner, release, err := s.provisioners.AcquireUserProvisioner(ctx, preview.Invite.MediaServerID)
	if err != nil {
		return Accepted{}, fmt.Errorf("acquire media user provisioner: %w", err)
	}
	defer release()
	failureID, err := core.NewID()
	if err != nil {
		return Accepted{}, err
	}
	state := acceptanceState{
		service: s, provisioner: provisioner, accountID: accountID,
		username: username, password: password, failureID: failureID,
	}
	defer state.clearPassword()
	linkCreated, err := s.store.RedeemInvite(ctx, hash, s.clock, state.redeem)
	if err != nil {
		if provisioningErr, ok := errors.AsType[*core.InviteProvisioningError](err); ok && provisioningErr != nil {
			return Accepted{}, err
		}
		err = state.compensate(ctx, err)
		return Accepted{}, state.persistPending(ctx, err)
	}
	if accountID != "" && !linkCreated {
		s.logger.InfoContext(ctx, "invite media user link kept existing mapping",
			"account_id", accountID, "media_server_id", preview.Invite.MediaServerID)
	}
	state.cleanup = false
	return Accepted{InviteID: preview.Invite.ID, MediaServerName: preview.MediaServerName, Username: username}, nil
}

func validateAcceptance(username, password string) error {
	if err := core.ValidateJellyfinUsername(username); err != nil {
		return err
	}
	return core.ValidateNewPassword(password)
}

func (s *Service) acceptancePreview(
	ctx context.Context, code string,
) (Preview, [sha256.Size]byte, error) {
	hash, valid := inviteLookupHash(code)
	preview, err := s.previewByHash(ctx, hash, valid)
	return preview, hash, err
}

type acceptanceState struct {
	service     *Service
	provisioner core.MediaUserProvisioner
	accountID   string
	username    string
	password    string
	failureID   string
	created     core.MediaUser
	invite      core.Invite
	cleanup     bool
	unresolved  bool
}

func (s *acceptanceState) redeem(ctx context.Context, invite core.Invite) (core.InviteRedemption, error) {
	s.invite = invite
	user, err := createMediaUser(ctx, s.provisioner, s.username, s.password)
	s.created = user
	if err != nil {
		s.captureCreationFailure(user, err)
		return core.InviteRedemption{}, s.finishProvisioningFailure(ctx, fmt.Errorf("create media user: %w", err), 1)
	}
	if !user.Created {
		s.unresolved = true
		return core.InviteRedemption{}, s.finishProvisioningFailure(ctx, core.ErrMediaUserCreateAmbiguous, 1)
	}
	s.cleanup = true
	allLibraries := len(invite.LibraryIDs) == 0
	if policyErr := setMediaUserLibraryAccess(ctx, s.provisioner, user.ID, invite.LibraryIDs, allLibraries); policyErr != nil {
		cause := fmt.Errorf("set media user library access: %w", policyErr)
		return core.InviteRedemption{}, s.finishProvisioningFailure(ctx, cause, 2)
	}
	id, err := core.NewID()
	if err != nil {
		return core.InviteRedemption{}, s.finishProvisioningFailure(ctx, err, 1)
	}
	return core.InviteRedemption{
		ID: id, InviteID: invite.ID, AccountID: s.accountID, MediaServerID: invite.MediaServerID,
		MediaUserID: user.ID, Username: s.username, RedeemedAt: core.NormalizeTime(s.service.clock.Now()),
	}, nil
}

func (s *acceptanceState) captureCreationFailure(user core.MediaUser, err error) {
	if user.Created {
		s.cleanup = true
		return
	}
	if user.ID != "" || errors.Is(err, core.ErrMediaUserCreateAmbiguous) {
		s.unresolved = true
	}
}

func (s *acceptanceState) finishProvisioningFailure(ctx context.Context, cause error, attempts int) error {
	for range attempts {
		cause = s.compensate(ctx, cause)
		if !s.cleanup {
			break
		}
	}
	reason, pending := s.pendingReason()
	if !pending {
		return cause
	}
	now := core.NormalizeTime(s.service.clock.Now())
	failure := s.provisioningFailure(s.failureID, reason, now)
	return &core.InviteProvisioningError{Failure: failure, Err: cause}
}

func (s *acceptanceState) compensate(ctx context.Context, cause error) error {
	if !s.cleanup {
		return cause
	}
	if !s.created.Created {
		s.cleanup, s.unresolved = false, true
		return cause
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
	defer cancel()
	if err := deleteMediaUser(cleanupCtx, s.provisioner, s.created.ID); err != nil {
		return errors.Join(cause, fmt.Errorf("%w: %w", core.ErrInviteCompensation, err))
	}
	s.cleanup = false
	return cause
}

func (s *acceptanceState) persistPending(ctx context.Context, cause error) error {
	reason, pending := s.pendingReason()
	if !pending {
		return cause
	}
	now := core.NormalizeTime(s.service.clock.Now())
	failure := s.provisioningFailure(s.failureID, reason, now)
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), compensationTimeout)
	defer cancel()
	err := s.service.store.RecordInviteProvisioningFailure(persistCtx, failure)
	return errors.Join(cause, core.ErrInviteProvisioningPending, err)
}

func (s *acceptanceState) provisioningFailure(
	id string, reason core.InviteProvisioningFailureReason, now time.Time,
) core.InviteProvisioningFailure {
	failure := core.InviteProvisioningFailure{
		ID: id, InviteID: s.invite.ID, MediaServerID: s.invite.MediaServerID,
		MediaUserID: s.created.ID, MediaUserOwned: s.created.Created, AccountID: s.accountID,
		Username: s.username, Reason: reason, CreatedAt: now, UpdatedAt: now,
	}
	if !failure.MediaUserOwned {
		failure.Terminal = true
		failure.LastError = core.InviteManualResolutionError
	}
	return failure
}

func (s *acceptanceState) pendingReason() (core.InviteProvisioningFailureReason, bool) {
	if s.cleanup {
		return core.InviteProvisioningCleanupFailed, true
	}
	if s.unresolved {
		return core.InviteProvisioningCreateAmbiguous, true
	}
	return "", false
}

func (s *acceptanceState) clearPassword() { s.password = "" }

func createMediaUser(
	ctx context.Context, provisioner core.MediaUserProvisioner, username, password string,
) (user core.MediaUser, err error) {
	defer func() {
		if recover() != nil {
			user = core.MediaUser{}
			err = errors.Join(core.ErrMediaUserCreateAmbiguous, errCreateUserPanic)
		}
	}()
	return provisioner.CreateUser(ctx, username, password)
}

func setMediaUserLibraryAccess(
	ctx context.Context, provisioner core.MediaUserProvisioner, userID string, libraryIDs []string, all bool,
) (err error) {
	defer func() {
		if recover() != nil {
			err = errSetAccessPanic
		}
	}()
	return provisioner.SetLibraryAccess(ctx, userID, libraryIDs, all)
}

func deleteMediaUser(ctx context.Context, provisioner core.MediaUserProvisioner, userID string) (err error) {
	defer func() {
		if recover() != nil {
			err = errDeleteUserPanic
		}
	}()
	return provisioner.DeleteUser(ctx, userID)
}
