package runtime

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/BonzTM/bloom/internal/config"
	"github.com/BonzTM/bloom/internal/core"
)

type recordingRoleReader struct {
	exists map[string]bool
	calls  []string
}

type cancelAwareRoleReader struct {
	hadDeadline bool
}

func (*cancelAwareRoleReader) ListRoles(context.Context, string, int) ([]core.Role, error) {
	return nil, nil
}

func (r *cancelAwareRoleReader) RoleExists(ctx context.Context, _ string) (bool, error) {
	_, r.hadDeadline = ctx.Deadline()
	<-ctx.Done()
	return false, ctx.Err()
}

func (*recordingRoleReader) ListRoles(context.Context, string, int) ([]core.Role, error) {
	return nil, nil
}

func (r *recordingRoleReader) RoleExists(_ context.Context, name string) (bool, error) {
	r.calls = append(r.calls, name)
	return r.exists[name], nil
}

func TestValidateOIDCRolesChecksEveryUniqueTarget(t *testing.T) {
	roles := &recordingRoleReader{exists: map[string]bool{"member": true, "owner": true}}
	cfg := config.OIDCConfig{
		Enabled: true, DefaultRole: "member",
		RoleMap: map[string]string{"admins": "owner", "users": "member"},
	}
	if err := validateOIDCRoles(context.Background(), roles, cfg); err != nil {
		t.Fatalf("validateOIDCRoles: %v", err)
	}
	if !slices.Equal(roles.calls, []string{"member", "owner"}) {
		t.Fatalf("role checks = %v, want every unique target", roles.calls)
	}
}

func TestValidateOIDCRolesRejectsMissingTarget(t *testing.T) {
	roles := &recordingRoleReader{exists: map[string]bool{"member": true}}
	cfg := config.OIDCConfig{
		Enabled: true, DefaultRole: "member",
		RoleMap: map[string]string{"admins": "missing-role"},
	}
	if err := validateOIDCRoles(context.Background(), roles, cfg); err == nil {
		t.Fatal("validateOIDCRoles accepted missing target")
	}
}

func TestValidateOIDCRolesBoundsDatabaseCalls(t *testing.T) {
	roles := &cancelAwareRoleReader{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := validateOIDCRoles(ctx, roles, config.OIDCConfig{
		Enabled: true, DefaultRole: "member",
	})
	if !errors.Is(err, context.Canceled) || !roles.hadDeadline {
		t.Fatalf("validateOIDCRoles error = %v, deadline = %t", err, roles.hadDeadline)
	}
}
