package core

import (
	"context"
	"errors"
	"testing"
)

type identityAccountStore struct {
	account   Account
	err       error
	updateErr error
	updated   string
	lookup    string
}

type blockingIdentityStore struct {
	account     Account
	blockLookup bool
	blockUpdate bool
}

func (s blockingIdentityStore) GetAccountByUsername(ctx context.Context, _ string) (Account, error) {
	if s.blockLookup {
		<-ctx.Done()
		return Account{}, ctx.Err()
	}
	return s.account, nil
}

func (s blockingIdentityStore) UpdateAccountPasswordHash(ctx context.Context, _, _ string) error {
	if s.blockUpdate {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (s *identityAccountStore) CreateAccount(context.Context, Account) error { return nil }
func (s *identityAccountStore) GetAccount(context.Context, string) (Account, error) {
	return Account{}, ErrNotFound
}

func (s *identityAccountStore) GetAccountByUsername(_ context.Context, username string) (Account, error) {
	s.lookup = username
	return s.account, s.err
}

func (s *identityAccountStore) UpdateAccountPasswordHash(_ context.Context, _, hash string) error {
	s.updated = hash
	return s.updateErr
}

func TestLocalIdentityProviderCanonicalizesLookup(t *testing.T) {
	t.Parallel()
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	store := &identityAccountStore{account: Account{ID: "acct-1", Username: "alice", PasswordHash: &hash}}
	provider := NewLocalIdentityProvider(store)
	if _, err := provider.Authenticate(context.Background(), "ＡLICE", "secret"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if store.lookup != "alice" {
		t.Fatalf("lookup username = %q, want alice", store.lookup)
	}
}

func TestLocalIdentityProviderRejectsInvalidUsername(t *testing.T) {
	t.Parallel()
	store := &identityAccountStore{}
	provider := NewLocalIdentityProvider(store)
	_, err := provider.Authenticate(context.Background(), "invalid name", "secret")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Authenticate = %v, want ErrInvalidCredentials", err)
	}
	if store.lookup != "" {
		t.Fatalf("invalid username reached store as %q", store.lookup)
	}
}

func TestLocalIdentityProviderOutcomes(t *testing.T) {
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	tests := []struct {
		name   string
		store  identityAccountStore
		pass   string
		reason CredentialFailureReason
	}{
		{name: "unknown user", store: identityAccountStore{err: ErrNotFound}, pass: "secret", reason: CredentialUnknownUser},
		{name: "bad password", store: identityAccountStore{account: Account{ID: "acct-1", Username: "alice", PasswordHash: &hash}}, pass: "wrong", reason: CredentialBadPassword},
		{name: "no local password", store: identityAccountStore{account: Account{ID: "acct-1", Username: "alice"}}, pass: "secret", reason: CredentialBadPassword},
		{name: "disabled", store: identityAccountStore{account: Account{ID: "acct-1", Username: "alice", PasswordHash: &hash, Disabled: true}}, pass: "secret", reason: CredentialDisabled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := tt.store
			derivations := 0
			provider := newLocalIdentityProvider(&store, func(password string, salt []byte, params passwordParams) ([]byte, error) {
				derivations++
				return derivePassword(password, salt, params), nil
			})
			_, err := provider.Authenticate(context.Background(), "alice", tt.pass)
			if derivations != 1 {
				t.Fatalf("Argon2id derivations = %d, want exactly 1", derivations)
			}
			var failure *CredentialFailure
			if !errors.As(err, &failure) || failure.Reason != tt.reason {
				t.Fatalf("Authenticate error = %v, want reason %q", err, tt.reason)
			}
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Authenticate error = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

func TestLocalIdentityProviderReturnsDummyDerivationFailure(t *testing.T) {
	t.Parallel()

	deriveErr := errors.New("argon2 unavailable")
	derivations := 0
	provider := newLocalIdentityProvider(&identityAccountStore{err: ErrNotFound},
		func(string, []byte, passwordParams) ([]byte, error) {
			derivations++
			return nil, deriveErr
		})
	_, err := provider.Authenticate(context.Background(), "alice", "secret")
	if !errors.Is(err, deriveErr) {
		t.Fatalf("Authenticate = %v, want wrapped derivation error", err)
	}
	if derivations != 1 {
		t.Fatalf("Argon2id derivations = %d, want exactly 1", derivations)
	}
}

func TestLocalIdentityProviderReturnsStoreFailure(t *testing.T) {
	t.Parallel()

	storeErr := errors.New("database unavailable")
	provider := NewLocalIdentityProvider(&identityAccountStore{err: storeErr})
	_, err := provider.Authenticate(context.Background(), "alice", "secret")
	if !errors.Is(err, storeErr) {
		t.Fatalf("Authenticate = %v, want wrapped store error", err)
	}
}

func TestLocalIdentityProviderRehashesAcceptedLegacyProfile(t *testing.T) {
	t.Parallel()

	legacy := supportedPasswordProfiles[1]
	salt := []byte("0123456789abcdef")
	hash := encodePasswordHash(legacy, salt, derivePassword("secret", salt, legacy))
	store := &identityAccountStore{account: Account{ID: "acct-1", Username: "alice", PasswordHash: &hash}}
	provider := NewLocalIdentityProvider(store)
	if _, err := provider.Authenticate(context.Background(), "alice", "secret"); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if store.updated == "" || PasswordHashNeedsUpgrade(store.updated) {
		t.Fatalf("updated hash = %q, want current profile", store.updated)
	}
}

func TestLocalIdentityProviderReturnsRehashFailure(t *testing.T) {
	t.Parallel()

	legacy := supportedPasswordProfiles[1]
	salt := []byte("0123456789abcdef")
	hash := encodePasswordHash(legacy, salt, derivePassword("secret", salt, legacy))
	storeErr := errors.New("update failed")
	store := &identityAccountStore{account: Account{ID: "acct-1", Username: "alice", PasswordHash: &hash}, updateErr: storeErr}
	provider := NewLocalIdentityProvider(store)
	if _, err := provider.Authenticate(context.Background(), "alice", "secret"); !errors.Is(err, storeErr) {
		t.Fatalf("Authenticate = %v, want wrapped update failure", err)
	}
}

func TestLocalIdentityProviderPropagatesContextToAccountStore(t *testing.T) {
	legacy := supportedPasswordProfiles[1]
	salt := []byte("0123456789abcdef")
	hash := encodePasswordHash(legacy, salt, derivePassword("secret", salt, legacy))
	tests := []struct {
		name  string
		store blockingIdentityStore
	}{
		{name: "lookup", store: blockingIdentityStore{blockLookup: true}},
		{name: "rehash persistence", store: blockingIdentityStore{
			account: Account{ID: "acct-1", Username: "alice", PasswordHash: &hash}, blockUpdate: true,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			provider := NewLocalIdentityProvider(tt.store)
			if _, err := provider.Authenticate(ctx, "alice", "secret"); !errors.Is(err, context.Canceled) {
				t.Fatalf("Authenticate = %v, want context cancellation", err)
			}
		})
	}
}
