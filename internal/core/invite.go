package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Invite validation and encoding bounds keep persisted and public input finite.
const (
	MaxInviteLabelBytes      = 100
	MaxInviteUses            = 1000
	MaxJellyfinUsernameBytes = 64
	InviteCodeBytes          = 16
	InviteCodeEncodedBytes   = 26
	MaxInviteLibraries       = MaxMediaServerLibraries
)

// InviteStatus is derived from persisted invite state at a caller-supplied instant.
type InviteStatus string

// Derived invite states are ordered by Status's precedence rules.
const (
	InviteActive    InviteStatus = "active"
	InviteExpired   InviteStatus = "expired"
	InviteExhausted InviteStatus = "exhausted"
	InviteRevoked   InviteStatus = "revoked"
)

// Invite grants bounded access to one registered media server.
type Invite struct {
	ID            string
	MediaServerID string
	CreatedBy     string
	Label         string
	ExpiresAt     *time.Time
	MaxUses       *int
	UseCount      int
	RevokedAt     *time.Time
	LibraryIDs    []string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Status derives the invite state without storing a second source of truth.
func (i Invite) Status(now time.Time) InviteStatus {
	if i.RevokedAt != nil {
		return InviteRevoked
	}
	if i.ExpiresAt != nil && !now.Before(*i.ExpiresAt) {
		return InviteExpired
	}
	if i.MaxUses != nil && i.UseCount >= *i.MaxUses {
		return InviteExhausted
	}
	return InviteActive
}

// InviteCursor is the stable newest-first keyset position.
type InviteCursor struct {
	CreatedAt time.Time
	ID        string
}

// InviteRedemption records the external account created by one acceptance.
type InviteRedemption struct {
	ID            string
	InviteID      string
	MediaServerID string
	MediaUserID   string
	Username      string
	RedeemedAt    time.Time
}

// InviteProvisioningFailure is a durable cleanup obligation for an acceptance
// that may have left a media-server user behind.
type InviteProvisioningFailure struct {
	ID            string
	InviteID      string
	MediaServerID string
	MediaUserID   string
	Username      string
	Reason        InviteProvisioningFailureReason
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// InviteProvisioningError carries the failure record that must be committed
// before the invite lock is released.
type InviteProvisioningError struct {
	Failure InviteProvisioningFailure
	Err     error
}

func (e *InviteProvisioningError) Error() string { return "media user cleanup pending" }

func (e *InviteProvisioningError) Unwrap() []error {
	return []error{ErrInviteProvisioningPending, e.Err}
}

// InviteProvisioningFailureReason is a finite persisted failure classification.
type InviteProvisioningFailureReason string

const (
	// InviteProvisioningCleanupFailed means bounded user deletion was exhausted.
	InviteProvisioningCleanupFailed InviteProvisioningFailureReason = "cleanup_failed"
	// InviteProvisioningCreateAmbiguous means account creation could not be resolved by lookup.
	InviteProvisioningCreateAmbiguous InviteProvisioningFailureReason = "ambiguous_create"
)

// InviteReader supplies administrative and public invite reads.
type InviteReader interface {
	GetInvite(ctx context.Context, id string) (Invite, error)
	GetInviteByCodeHash(ctx context.Context, codeHash [sha256.Size]byte) (Invite, error)
	ListInvites(ctx context.Context, after *InviteCursor, pageSize int) ([]Invite, error)
}

// InviteStore applies invite writes. RedeemInvite acquires the engine-specific
// invite lock before reading clock and holds that lock while redeem performs the
// external operation. It commits either the redemption and use count or a
// provisioning failure supplied by redeem before releasing the lock.
type InviteStore interface {
	CreateInvite(ctx context.Context, invite Invite, codeHash [sha256.Size]byte) error
	RevokeInvite(ctx context.Context, id string, revokedAt time.Time) (Invite, error)
	RedeemInvite(ctx context.Context, codeHash [sha256.Size]byte, clock Clock, redeem InviteRedeemFunc) error
	RecordInviteProvisioningFailure(ctx context.Context, failure InviteProvisioningFailure) error
}

// InviteRedeemFunc performs the external account work under the invite lock.
type InviteRedeemFunc func(context.Context, Invite) (InviteRedemption, error)

// MediaUser is the stable identity returned after creating a media-server user.
type MediaUser struct {
	ID   string
	Name string
}

// MediaUserProvisioner is the consumer-owned adapter seam for invite acceptance.
type MediaUserProvisioner interface {
	CreateUser(ctx context.Context, name, password string) (MediaUser, error)
	SetLibraryAccess(ctx context.Context, userID string, libraryIDs []string, all bool) error
	DeleteUser(ctx context.Context, userID string) error
}

// MediaUserNameError classifies Jellyfin's 400 response to user creation.
type MediaUserNameError struct{ Err error }

func (e *MediaUserNameError) Error() string { return "media-server username is unavailable" }
func (e *MediaUserNameError) Unwrap() error { return e.Err }

var (
	// ErrInviteUnavailable is deliberately opaque across every unusable-code case.
	ErrInviteUnavailable = errors.New("invite not found")
	// ErrInviteCompensation reports failure to remove a partly provisioned user.
	ErrInviteCompensation = errors.New("media user cleanup failed")
	// ErrInviteProvisioningPending reports a durable unresolved cleanup obligation.
	ErrInviteProvisioningPending = errors.New("media user cleanup pending")
	// ErrInviteProvisioningFailureRecord reports that the durable failure insert failed.
	ErrInviteProvisioningFailureRecord = errors.New("record media user cleanup failure")
	// ErrMediaUserCreateAmbiguous reports that an upstream create may have succeeded.
	ErrMediaUserCreateAmbiguous = errors.New("media user creation outcome is ambiguous")
)

// ValidateInvite validates state supplied at creation time.
func ValidateInvite(invite Invite, now time.Time) error {
	if !ValidID(invite.ID) || !ValidID(invite.MediaServerID) || !ValidID(invite.CreatedBy) {
		return ErrInvalidArgument
	}
	if err := ValidateInviteLabel(invite.Label); err != nil {
		return err
	}
	if invite.ExpiresAt != nil && !invite.ExpiresAt.After(now) {
		return ErrInvalidArgument
	}
	if invite.MaxUses != nil && (*invite.MaxUses < 1 || *invite.MaxUses > MaxInviteUses) {
		return ErrInvalidArgument
	}
	if len(invite.LibraryIDs) > MaxInviteLibraries || hasInvalidLibraryID(invite.LibraryIDs) {
		return ErrInvalidArgument
	}
	return nil
}

// ValidateInviteLabel validates the operator-visible label.
func ValidateInviteLabel(label string) error {
	if label == "" || len(label) > MaxInviteLabelBytes || !utf8.ValidString(label) || label != strings.TrimSpace(label) {
		return ErrInvalidArgument
	}
	if strings.IndexFunc(label, unicode.IsControl) >= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func hasInvalidLibraryID(ids []string) bool {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" || len(id) > 128 || !utf8.ValidString(id) || strings.IndexFunc(id, unicode.IsControl) >= 0 {
			return true
		}
		if _, exists := seen[id]; exists {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}

// ValidateJellyfinUsername follows Jellyfin's current server-side rule:
// ^(?!\s)[\w\ \-'._@+]+(?<!\s)$, where .NET \w accepts Unicode letters,
// non-spacing marks, decimal digits, and connector punctuation. Jellyfin also
// rejects "." and ".." explicitly. Source:
// https://github.com/jellyfin/jellyfin/blob/master/Jellyfin.Server.Implementations/Users/UserManager.cs#L885-L891
// The byte bound is Bloom's own: Jellyfin's rule has no length limit, but a
// bound keeps request bodies, audit records, and rate-limit keys small.
func ValidateJellyfinUsername(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > MaxJellyfinUsernameBytes ||
		!utf8.ValidString(name) || name != strings.TrimSpace(name) {
		return ErrInvalidArgument
	}
	for _, r := range name {
		if unicode.IsControl(r) || !jellyfinUsernameRune(r) {
			return ErrInvalidArgument
		}
	}
	return nil
}

func jellyfinUsernameRune(r rune) bool {
	if r == ' ' || strings.ContainsRune("-'._@+", r) {
		return true
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Pc, r)
}

// NewInviteCode returns 128 random bits encoded as canonical unpadded base32.
func NewInviteCode() (string, [sha256.Size]byte, error) {
	var raw [InviteCodeBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", [sha256.Size]byte{}, fmt.Errorf("generate invite code: %w", err)
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:])
	return code, sha256.Sum256([]byte(code)), nil
}

// ParseInviteCode validates the canonical public code and returns its storage digest.
func ParseInviteCode(code string) ([sha256.Size]byte, error) {
	if len(code) != InviteCodeEncodedBytes {
		return [sha256.Size]byte{}, ErrInviteUnavailable
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(code)
	if err != nil || len(decoded) != InviteCodeBytes {
		return [sha256.Size]byte{}, ErrInviteUnavailable
	}
	if base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(decoded) != code {
		return [sha256.Size]byte{}, ErrInviteUnavailable
	}
	return sha256.Sum256([]byte(code)), nil
}
