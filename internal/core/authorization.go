package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxRoleNameBytes is the role-name domain byte limit.
const MaxRoleNameBytes = 64

// Permission is a stable authorization capability identifier. Published
// identifiers are part of the API contract and may only be added, never
// renamed or removed.
type Permission string

const (
	// PermissionUsersRead allows reading user summaries.
	PermissionUsersRead Permission = "users.read"
	// PermissionUsersInvite allows inviting users.
	PermissionUsersInvite Permission = "users.invite"
	// PermissionUsersManage allows managing user accounts.
	PermissionUsersManage Permission = "users.manage"
	// PermissionRequestsReadOwn allows reading requests owned by the account.
	PermissionRequestsReadOwn Permission = "requests.read.own"
	// PermissionRequestsCreate allows creating requests.
	PermissionRequestsCreate Permission = "requests.create"
	// PermissionRequestsApprove allows approving requests.
	PermissionRequestsApprove Permission = "requests.approve"
	// PermissionStatsReadOwn allows reading statistics owned by the account.
	PermissionStatsReadOwn Permission = "stats.read.own"
	// PermissionStatsReadAll allows reading all statistics.
	PermissionStatsReadAll Permission = "stats.read.all"
	// PermissionAdminSettings allows managing application settings.
	PermissionAdminSettings Permission = "admin.settings"
	// PermissionAdminRoles allows reading and managing roles.
	PermissionAdminRoles Permission = "admin.roles"
)

// PermissionDefinition describes one entry in the public permission catalog.
type PermissionDefinition struct {
	ID     Permission
	Module string
}

// CatalogPermission is a permission proven to belong to the published finite
// catalog. Its value can be created only through NewCatalogPermission.
type CatalogPermission struct {
	permission Permission
}

// NewCatalogPermission validates p against the published permission catalog.
func NewCatalogPermission(p Permission) (CatalogPermission, error) {
	if !p.Valid() {
		return CatalogPermission{}, fmt.Errorf("permission %q: %w", p, ErrInvalidArgument)
	}
	return CatalogPermission{permission: p}, nil
}

// Permission returns the validated catalog identifier.
func (p CatalogPermission) Permission() Permission { return p.permission }

var permissionCatalog = [...]PermissionDefinition{
	{ID: PermissionUsersRead, Module: "users"},
	{ID: PermissionUsersInvite, Module: "users"},
	{ID: PermissionUsersManage, Module: "users"},
	{ID: PermissionRequestsReadOwn, Module: "requests"},
	{ID: PermissionRequestsCreate, Module: "requests"},
	{ID: PermissionRequestsApprove, Module: "requests"},
	{ID: PermissionStatsReadOwn, Module: "stats"},
	{ID: PermissionStatsReadAll, Module: "stats"},
	{ID: PermissionAdminSettings, Module: "admin"},
	{ID: PermissionAdminRoles, Module: "admin"},
}

// PermissionCatalog returns a copy of the stable catalog in contract order.
func PermissionCatalog() []PermissionDefinition {
	return slices.Clone(permissionCatalog[:])
}

// Valid reports whether p is a published permission identifier.
func (p Permission) Valid() bool {
	return slices.ContainsFunc(permissionCatalog[:], func(entry PermissionDefinition) bool {
		return entry.ID == p
	})
}

// Module returns the catalog module for p, or an empty string when p is not
// published.
func (p Permission) Module() string {
	for _, entry := range permissionCatalog {
		if entry.ID == p {
			return entry.Module
		}
	}
	return ""
}

// Role is a named set of permissions assigned to accounts.
type Role struct {
	ID          string
	Name        string
	Description string
	BuiltIn     bool
	CreatedAt   time.Time
	Permissions []Permission
}

// AuthorizationSnapshot is one account's role names and effective permissions
// read from one coherent database state.
type AuthorizationSnapshot struct {
	RoleNames   []string
	Permissions []Permission
}

// ValidRoleName reports whether name belongs to the role-name domain.
func ValidRoleName(name string) bool {
	return name != "" && len(name) <= MaxRoleNameBytes && utf8.ValidString(name) &&
		name == strings.TrimSpace(name) && strings.IndexFunc(name, unicode.IsControl) == -1
}

// Authorizer is the per-request authorization decision seam.
type Authorizer interface {
	Permissions(ctx context.Context, accountID string) ([]Permission, error)
	Snapshot(ctx context.Context, accountID string) (AuthorizationSnapshot, error)
}

// RoleReader supplies role data to administrative API responses.
type RoleReader interface {
	ListRoles(ctx context.Context, afterName string, pageSize int) ([]Role, error)
}

// AdminAccountStore applies operator-driven account-role changes atomically.
type AdminAccountStore interface {
	CreateAccountWithRole(ctx context.Context, account Account, roleName string) error
	GrantRole(ctx context.Context, accountID, roleName string) (bool, error)
}

// ErrForbidden reports a denied authorization decision.
var ErrForbidden = errors.New("forbidden")

// OwnsResource reports whether account owns an independently loaded resource.
// Transport code must not use it until a real owned resource exists; core
// operations call it only after loading that resource's owner identifier.
func OwnsResource(account Account, ownerID string) bool {
	return account.ID != "" && ownerID != "" && account.ID == ownerID
}
