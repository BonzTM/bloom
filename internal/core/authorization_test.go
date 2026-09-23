package core_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/BonzTM/bloom/internal/core"
)

func TestValidRoleName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "ASCII", value: "owner", want: true},
		{name: "Unicode", value: "opérateur", want: true},
		{name: "empty"},
		{name: "leading space", value: " owner"},
		{name: "trailing space", value: "owner "},
		{name: "NUL", value: "owner\x00"},
		{name: "control", value: "owner\n"},
		{name: "invalid UTF-8", value: string([]byte{0xff})},
		{name: "oversized", value: strings.Repeat("r", core.MaxRoleNameBytes+1)},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := core.ValidRoleName(testCase.value); got != testCase.want {
				t.Fatalf("ValidRoleName(%q) = %v, want %v", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestPermissionCatalogIsStableAndUnique(t *testing.T) {
	t.Parallel()
	want := []core.PermissionDefinition{
		{ID: "users.read", Module: "users"},
		{ID: "users.invite", Module: "users"},
		{ID: "users.manage", Module: "users"},
		{ID: "requests.read.own", Module: "requests"},
		{ID: "requests.create", Module: "requests"},
		{ID: "requests.approve", Module: "requests"},
		{ID: "stats.read.own", Module: "stats"},
		{ID: "stats.read.all", Module: "stats"},
		{ID: "admin.settings", Module: "admin"},
		{ID: "admin.roles", Module: "admin"},
	}
	catalog := core.PermissionCatalog()
	seen := make(map[core.Permission]bool, len(catalog))
	for _, entry := range catalog {
		if seen[entry.ID] {
			t.Fatalf("duplicate permission %q", entry.ID)
		}
		seen[entry.ID] = true
		if !entry.ID.Valid() || entry.ID.Module() != entry.Module {
			t.Errorf("invalid catalog entry %+v", entry)
		}
	}
	if !slices.Equal(catalog, want) {
		t.Fatalf("permission catalog = %v, want stable golden %v", catalog, want)
	}
}

func TestOwnsResource(t *testing.T) {
	t.Parallel()
	account := core.Account{ID: "account-1"}
	if !core.OwnsResource(account, "account-1") {
		t.Fatal("matching resource owner was denied")
	}
	for _, ownerID := range []string{"", "account-2"} {
		if core.OwnsResource(account, ownerID) {
			t.Errorf("owner %q was accepted", ownerID)
		}
	}
	if core.OwnsResource(core.Account{}, "account-1") {
		t.Fatal("empty account id was accepted")
	}
}

func TestCatalogPermissionRejectsUnpublishedValues(t *testing.T) {
	t.Parallel()
	permission, err := core.NewCatalogPermission(core.PermissionAdminRoles)
	if err != nil || permission.Permission() != core.PermissionAdminRoles {
		t.Fatalf("NewCatalogPermission(admin.roles) = %v, %v", permission, err)
	}
	if _, err := core.NewCatalogPermission("invented.permission"); err == nil {
		t.Fatal("NewCatalogPermission accepted an unpublished value")
	}
}
