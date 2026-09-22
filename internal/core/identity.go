package core

import (
	"context"
	"errors"
	"fmt"
)

// ErrInvalidCredentials is the opaque authentication failure observed by
// callers that do not need the internal reason.
var ErrInvalidCredentials = errors.New("invalid credentials")

// CredentialFailureReason distinguishes authentication failures for audit
// records while the HTTP boundary collapses them to one response.
type CredentialFailureReason string

const (
	// CredentialUnknownUser means no account matched the supplied username.
	CredentialUnknownUser CredentialFailureReason = "unknown_user"
	// CredentialBadPassword means the local password did not match or was absent.
	CredentialBadPassword CredentialFailureReason = "bad_password"
	// CredentialDisabled means the matching account is disabled.
	CredentialDisabled CredentialFailureReason = "disabled"
)

// CredentialFailure carries an audit-safe reason and unwraps to the opaque
// ErrInvalidCredentials category.
type CredentialFailure struct {
	Reason CredentialFailureReason
}

func (e *CredentialFailure) Error() string { return ErrInvalidCredentials.Error() }
func (e *CredentialFailure) Unwrap() error { return ErrInvalidCredentials }

// IdentityProvider authenticates one username and password pair. Provider
// implementations remain concurrent and pluggable per ADR 0006.
type IdentityProvider interface {
	Authenticate(ctx context.Context, username, password string) (Account, error)
}

// LocalIdentityStore is the narrow persistence seam needed by local login.
type LocalIdentityStore interface {
	GetAccountByUsername(ctx context.Context, username string) (Account, error)
	UpdateAccountPasswordHash(ctx context.Context, id, hash string) error
}

// LocalIdentityProvider authenticates against password hashes in AccountStore.
type LocalIdentityProvider struct {
	accounts       LocalIdentityStore
	derivePassword passwordDeriveFunc
}

// NewLocalIdentityProvider returns the local username/password provider.
func NewLocalIdentityProvider(accounts LocalIdentityStore) *LocalIdentityProvider {
	return newLocalIdentityProvider(accounts, realPasswordDerivation)
}

func newLocalIdentityProvider(accounts LocalIdentityStore, derive passwordDeriveFunc) *LocalIdentityProvider {
	return &LocalIdentityProvider{accounts: accounts, derivePassword: derive}
}

func (p *LocalIdentityProvider) performPasswordWork(encoded *string, password string) (bool, error) {
	if encoded == nil {
		return false, consumePasswordWork(password, p.derivePassword)
	}
	return verifyPassword(*encoded, password, p.derivePassword)
}

// Authenticate performs one Argon2id calculation for every credential outcome.
func (p *LocalIdentityProvider) Authenticate(ctx context.Context, username, password string) (Account, error) {
	key, err := UsernameKey(username)
	if err != nil {
		return p.rejectUnknownCredential(password)
	}
	account, err := p.accounts.GetAccountByUsername(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return p.rejectUnknownCredential(password)
	}
	if err != nil {
		return Account{}, fmt.Errorf("load local identity: %w", err)
	}
	matched, err := p.performPasswordWork(account.PasswordHash, password)
	if err != nil {
		return Account{}, fmt.Errorf("verify local identity: %w", err)
	}
	if account.Disabled {
		return Account{}, &CredentialFailure{Reason: CredentialDisabled}
	}
	if account.PasswordHash == nil || !matched {
		return Account{}, &CredentialFailure{Reason: CredentialBadPassword}
	}
	if PasswordHashNeedsUpgrade(*account.PasswordHash) {
		if err := p.upgradePasswordHash(ctx, account.ID, password); err != nil {
			return Account{}, err
		}
	}
	return account, nil
}

func (p *LocalIdentityProvider) rejectUnknownCredential(password string) (Account, error) {
	if _, err := p.performPasswordWork(nil, password); err != nil {
		return Account{}, fmt.Errorf("verify unknown local identity: %w", err)
	}
	return Account{}, &CredentialFailure{Reason: CredentialUnknownUser}
}

func (p *LocalIdentityProvider) upgradePasswordHash(ctx context.Context, accountID, password string) error {
	hash, err := hashPassword(password, p.derivePassword)
	if err != nil {
		return fmt.Errorf("rehash local identity: %w", err)
	}
	if err := p.accounts.UpdateAccountPasswordHash(ctx, accountID, hash); err != nil {
		return fmt.Errorf("persist local identity rehash: %w", err)
	}
	return nil
}
