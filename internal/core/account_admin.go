package core

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/secure/precis"
)

const (
	// MaxAccountListPageSize bounds one administrative account page.
	MaxAccountListPageSize = 100
	// MaxAccountRoleAssignments bounds roles returned for one account.
	MaxAccountRoleAssignments = 100
	// MaxAccountLinkedMediaUsers bounds media-user links returned for one account.
	MaxAccountLinkedMediaUsers = 100
	// MaxAccountSearchBytes bounds a submitted username substring before normalization.
	MaxAccountSearchBytes = 128
	// MaxAccountListCursorBytes bounds an opaque public account-list cursor.
	MaxAccountListCursorBytes = 2048
	maxAccountSearchKeyBytes  = MaxAccountSearchBytes * 4
)

// AccountSignInMethod describes the credential kinds linked to an account.
type AccountSignInMethod string

const (
	// AccountSignInLocal means the account has a local password only.
	AccountSignInLocal AccountSignInMethod = "local"
	// AccountSignInOIDC means the account has one or more OIDC identities only.
	AccountSignInOIDC AccountSignInMethod = "oidc"
	// AccountSignInBoth means the account has local and OIDC credentials.
	AccountSignInBoth AccountSignInMethod = "both"
)

// AccountRoleSource records how one role was assigned to an account.
type AccountRoleSource string

const (
	// AccountRoleSourceManual records an operator-managed assignment.
	AccountRoleSourceManual AccountRoleSource = "manual"
	// AccountRoleSourceOIDC records an assignment mapped from OIDC claims.
	AccountRoleSourceOIDC AccountRoleSource = "oidc"
)

// AccountRoleAssignment is one role and its independent assignment source.
type AccountRoleAssignment struct {
	Name   string
	Source AccountRoleSource
}

// AdminAccount is the secret-free administrative read model for one account.
type AdminAccount struct {
	ID, Username, UsernameKey string
	CreatedAt                 time.Time
	SignInMethod              AccountSignInMethod
	Roles                     []AccountRoleAssignment
	MediaUsers                []AccountMediaUser
}

// AccountListPosition is the stable username-key and id pagination tuple.
type AccountListPosition struct {
	UsernameKey string
	ID          string
}

// AccountListQuery selects one bounded administrative account page.
type AccountListQuery struct {
	SearchKey string
	After     *AccountListPosition
	Limit     int
}

// Valid reports whether the query is safe for both storage engines.
func (q AccountListQuery) Valid() bool {
	if q.Limit < 1 || q.Limit > MaxAccountListPageSize ||
		len(q.SearchKey) > maxAccountSearchKeyBytes || !utf8.ValidString(q.SearchKey) {
		return false
	}
	normalized, err := normalizeAccountSearchKey(q.SearchKey)
	if err != nil || normalized != q.SearchKey {
		return false
	}
	if q.After == nil {
		return true
	}
	key, err := UsernameKey(q.After.UsernameKey)
	return err == nil && key == q.After.UsernameKey && ValidID(q.After.ID)
}

// AccountPage is one bounded page plus a lookahead indicator.
type AccountPage struct {
	Items   []AdminAccount
	HasMore bool
}

// AccountAdminReader supplies secret-free account administration reads.
type AccountAdminReader interface {
	ListAccounts(ctx context.Context, query AccountListQuery) (AccountPage, error)
	GetAdminAccount(ctx context.Context, id string) (AdminAccount, error)
}

// AccountSearchKey normalizes a bounded username substring for username_key matching.
func AccountSearchKey(value string) (string, error) {
	if len(value) > MaxAccountSearchBytes || !utf8.ValidString(value) {
		return "", ErrInvalidArgument
	}
	return normalizeAccountSearchKey(value)
}

func normalizeAccountSearchKey(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	// Control characters never form a username and PostgreSQL cannot store NUL,
	// so reject them here where every reader's validation converges.
	if strings.ContainsFunc(value, unicode.IsControl) {
		return "", ErrInvalidArgument
	}
	key, err := precis.UsernameCaseMapped.CompareKey(value)
	if err != nil {
		key = strings.ToLower(value)
	}
	if len(key) > maxAccountSearchKeyBytes {
		return "", ErrInvalidArgument
	}
	return key, nil
}

// SignInMethod derives the public credential enum without returning credential material.
func SignInMethod(hasLocal, hasOIDC bool) (AccountSignInMethod, error) {
	switch {
	case hasLocal && hasOIDC:
		return AccountSignInBoth, nil
	case hasLocal:
		return AccountSignInLocal, nil
	case hasOIDC:
		return AccountSignInOIDC, nil
	default:
		return "", ErrInvalidArgument
	}
}

// Valid reports whether the stored role-assignment source is recognized.
func (s AccountRoleSource) Valid() bool {
	return s == AccountRoleSourceManual || s == AccountRoleSourceOIDC
}
