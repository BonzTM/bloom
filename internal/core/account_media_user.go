package core

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrMediaUserNotLinked reports that an account has no usable media-user link.
	ErrMediaUserNotLinked = errors.New("media user not linked")
	// ErrMediaUserAmbiguous reports multiple exact upstream username matches.
	ErrMediaUserAmbiguous = errors.New("media user lookup is ambiguous")
)

const (
	// MaxAccountMediaUserIDBytes bounds an upstream media-user identifier.
	MaxAccountMediaUserIDBytes = 128
	// MaxMediaUsernameBytes bounds a stored media-server username snapshot.
	MaxMediaUsernameBytes = 64
	// MaxAccountMediaUsers bounds one account-media-user list response.
	MaxAccountMediaUsers = 100
)

// AccountMediaUserSource records how an account-to-media-user link was established.
type AccountMediaUserSource string

const (
	// AccountMediaUserSourceInvite records a signed-in invite acceptance.
	AccountMediaUserSourceInvite AccountMediaUserSource = "invite"
	// AccountMediaUserSourceMatch records an exact on-demand username match.
	AccountMediaUserSourceMatch AccountMediaUserSource = "match"
	// AccountMediaUserSourceAdmin records an administrator assignment.
	AccountMediaUserSourceAdmin AccountMediaUserSource = "admin"
)

// AccountMediaUser links one Bloom account to one user on one media server.
type AccountMediaUser struct {
	AccountID       string
	MediaServerID   string
	MediaServerName string
	MediaUserID     string
	Username        string
	Source          AccountMediaUserSource
	CreatedAt       time.Time
	UpdatedAt       time.Time
	SuppressedAt    *time.Time
}

// ValidateAccountMediaUser validates a link before persistence.
func ValidateAccountMediaUser(link AccountMediaUser) error {
	if !ValidID(link.AccountID) || !ValidID(link.MediaServerID) {
		return ErrInvalidArgument
	}
	if !validBoundedText(link.MediaUserID, MaxAccountMediaUserIDBytes) ||
		!validBoundedText(link.Username, MaxMediaUsernameBytes) {
		return ErrInvalidArgument
	}
	if link.Source != AccountMediaUserSourceInvite && link.Source != AccountMediaUserSourceMatch &&
		link.Source != AccountMediaUserSourceAdmin {
		return ErrInvalidArgument
	}
	if link.CreatedAt.IsZero() || link.UpdatedAt.IsZero() || link.UpdatedAt.Before(link.CreatedAt) {
		return ErrInvalidArgument
	}
	if link.SuppressedAt != nil && link.SuppressedAt.Before(link.CreatedAt) {
		return ErrInvalidArgument
	}
	return nil
}

// ValidAccountMediaUserID reports whether an upstream user identifier is safe to persist.
func ValidAccountMediaUserID(value string) bool {
	return validBoundedText(value, MaxAccountMediaUserIDBytes)
}

// ValidMediaUsername reports whether a media-server username is safe to snapshot.
func ValidMediaUsername(value string) bool {
	return validBoundedText(value, MaxMediaUsernameBytes)
}

func validBoundedText(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

// AccountMediaUserReader reads account-to-media-user links.
type AccountMediaUserReader interface {
	GetAccountMediaUser(ctx context.Context, accountID, mediaServerID string) (AccountMediaUser, error)
	ListAccountMediaUsers(ctx context.Context, accountID string, includeSuppressed bool, limit int) ([]AccountMediaUser, error)
}

// AccountMediaUserWriter changes account-to-media-user links.
type AccountMediaUserWriter interface {
	SetAccountMediaUser(ctx context.Context, link AccountMediaUser) error
	CreateAccountMediaUserIfAbsent(ctx context.Context, link AccountMediaUser) (bool, error)
	SuppressAccountMediaUser(ctx context.Context, accountID, mediaServerID string, suppressedAt time.Time) error
}
