package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

const (
	oidcProviderName      = "oidc"
	maxOIDCUsernameTrials = 10
)

// NewOIDCAccountStore returns the atomic OIDC identity/account store.
func NewOIDCAccountStore(pool *sql.DB, driver config.Driver) (core.OIDCAccountStore, error) {
	switch driver {
	case config.DriverSQLite:
		return newSQLiteOIDCStore(pool), nil
	case config.DriverPostgres:
		return newPostgresOIDCStore(pool), nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func validateOIDCSignIn(signIn core.OIDCSignIn) error {
	if signIn.Provider != oidcProviderName || signIn.Issuer == "" || len(signIn.Issuer) > core.MaxOIDCIssuerBytes ||
		signIn.Subject == "" || len(signIn.Subject) > core.MaxOIDCSubjectBytes ||
		signIn.UsernameClaim == "" || len(signIn.UsernameClaim) > 1024 || signIn.Now.IsZero() {
		return fmt.Errorf("OIDC sign-in: %w", core.ErrInvalidArgument)
	}
	if !signIn.AutoProvision && signIn.DefaultRole != "" {
		return fmt.Errorf("OIDC sign-in provisioning state: %w", core.ErrInvalidArgument)
	}
	if signIn.AutoProvision && !core.ValidRoleName(signIn.DefaultRole) {
		return fmt.Errorf("OIDC sign-in default role: %w", core.ErrInvalidArgument)
	}
	if len(signIn.MappedRoles) > 64 {
		return fmt.Errorf("OIDC sign-in roles: %w", core.ErrInvalidArgument)
	}
	for _, role := range signIn.MappedRoles {
		if !core.ValidRoleName(role) {
			return fmt.Errorf("OIDC sign-in role: %w", core.ErrInvalidArgument)
		}
	}
	return nil
}

func encodeMappedRoles(roles []string) (string, error) {
	roles = slices.Clone(roles)
	if roles == nil {
		roles = []string{}
	}
	slices.Sort(roles)
	roles = slices.Compact(roles)
	encoded, err := json.Marshal(roles)
	if err != nil {
		return "", fmt.Errorf("encode mapped roles: %w", err)
	}
	return string(encoded), nil
}

func decodeMappedRoles(encoded string) ([]string, error) {
	var roles []string
	if err := json.Unmarshal([]byte(encoded), &roles); err != nil {
		return nil, fmt.Errorf("decode stored mapped roles: %w", err)
	}
	for _, role := range roles {
		if !core.ValidRoleName(role) {
			return nil, fmt.Errorf("invalid stored mapped role %q", role)
		}
	}
	slices.Sort(roles)
	return slices.Compact(roles), nil
}

func provisionRoles(signIn core.OIDCSignIn) []string {
	if len(signIn.MappedRoles) > 0 {
		return slices.Clone(signIn.MappedRoles)
	}
	return []string{signIn.DefaultRole}
}

func oidcRoleLookupError(role string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("configured OIDC role %q is unavailable", role)
	}
	return fmt.Errorf("find configured OIDC role %q: %w", role, err)
}

type oidcAccountCreator interface {
	CreateAccount(context.Context, core.Account) error
}

func createOIDCAccount(
	ctx context.Context, creator oidcAccountCreator, signIn core.OIDCSignIn, collision int,
) (core.Account, error) {
	username, err := core.OIDCUsername(signIn.UsernameClaim, signIn.Issuer+"\x00"+signIn.Subject, collision)
	if err != nil {
		return core.Account{}, fmt.Errorf("derive OIDC username: %w", err)
	}
	id, err := core.NewID()
	if err != nil {
		return core.Account{}, err
	}
	account := core.Account{ID: id, Username: username, CreatedAt: signIn.Now}
	if err := creator.CreateAccount(ctx, account); err != nil {
		return core.Account{}, err
	}
	return account, nil
}
