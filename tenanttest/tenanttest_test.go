package tenanttest

import (
	"context"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithFakeTenant(t *testing.T) {
	ctx := WithFakeTenant(context.Background(), "tenant-abc", tenant.Active)

	got := tenantctx.FromContext(ctx)
	require.NotNil(t, got)
	assert.Equal(t, tenant.TenantID("tenant-abc"), got.ID)
	assert.Equal(t, tenant.Active, got.State)
	assert.Empty(t, got.Roles)
}

func TestWithFakeTenantFull(t *testing.T) {
	expected := &tenant.Tenant{
		ID:    "tenant-admin",
		State: tenant.Active,
		Roles: []tenant.Role{"admin", "manager"},
	}

	ctx := WithFakeTenantFull(context.Background(), expected)

	got := tenantctx.FromContext(ctx)
	require.NotNil(t, got)
	assert.Equal(t, expected.ID, got.ID)
	assert.Equal(t, expected.State, got.State)
	assert.Equal(t, expected.Roles, got.Roles)
}