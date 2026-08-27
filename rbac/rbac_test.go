package rbac

import (
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

func TestRBAC_Can(t *testing.T) {
	rbac := New()

	// Given: an "admin" role with several permissions
	rbac.DefineRole(
		"tenant-A",
		"admin",
		"users:read",
		"users:write",
	)

	tenantA := &tenant.Tenant{
		ID:    "tenant-A",
		Roles: []tenant.Role{"admin"},
	}

	// Then: the tenant has the permission
	assert.True(t, rbac.Can(tenantA, "users:write"))

	// And: the tenant does not have a permission that doesn't exist
	assert.False(t, rbac.Can(tenantA, "users:delete"))
}

func TestRBAC_UnknownTenantOrRole(t *testing.T) {
	rbac := New()

	// Tenant-A has the admin role
	rbac.DefineRole(
		"tenant-A",
		"admin",
		"users:read",
	)

	// Tenant-B has no RBAC definitions
	tenantB := &tenant.Tenant{
		ID:    "tenant-B",
		Roles: []tenant.Role{"admin"},
	}

	// The tenant does not exist in the definitions
	assert.False(t, rbac.Can(tenantB, "users:read"))

	// Tenant-A has a role that doesn't exist
	tenantA := &tenant.Tenant{
		ID:    "tenant													-A",
		Roles: []tenant.Role{"viewer"},
	}

	assert.False(t, rbac.Can(tenantA, "users:read"))
}

func TestRBAC_Can_NoRoleGrantsPermission(t *testing.T) {
	rbac := New()
	rbac.DefineRole("tenant-A", "editor", "posts:write")
	rbac.DefineRole("tenant-A", "viewer", "posts:read")

	// The tenant holds two defined roles, but neither grants this
	// permission — Can must not fall back to "any role, any permission".
	tenantA := &tenant.Tenant{ID: "tenant-A", Roles: []tenant.Role{"editor", "viewer"}}

	assert.False(t, rbac.Can(tenantA, "posts:delete"))
}

func TestRBAC_Can_EmptyPermission(t *testing.T) {
	rbac := New()
	rbac.DefineRole("tenant-A", "admin", "users:read")

	tenantA := &tenant.Tenant{ID: "tenant-A", Roles: []tenant.Role{"admin"}}

	// An empty permission was never granted, and must not be treated as a
	// wildcard or match anything by accident of zero-value comparison.
	assert.False(t, rbac.Can(tenantA, ""))
}

func TestRBAC_Can_PartialRoleCoverage(t *testing.T) {
	rbac := New()
	rbac.DefineRole("tenant-A", "support", "tickets:read")
	rbac.DefineRole("tenant-A", "billing", "invoices:read")

	// Holding both roles grants the permission because "billing" alone is
	// sufficient — even though "support" alone would not be.
	both := &tenant.Tenant{ID: "tenant-A", Roles: []tenant.Role{"support", "billing"}}
	assert.True(t, rbac.Can(both, "invoices:read"))

	// The insufficient role alone must not grant it.
	supportOnly := &tenant.Tenant{ID: "tenant-A", Roles: []tenant.Role{"support"}}
	assert.False(t, rbac.Can(supportOnly, "invoices:read"))

	// The sufficient role alone is, on its own, enough.
	billingOnly := &tenant.Tenant{ID: "tenant-A", Roles: []tenant.Role{"billing"}}
	assert.True(t, rbac.Can(billingOnly, "invoices:read"))
}

func TestRBAC_Can_RoleIsolatedPerTenant(t *testing.T) {
	rbac := New()
	// Same role name, "admin", means something completely different for
	// each tenant — definitions are never shared across tenants.
	rbac.DefineRole("tenant-A", "admin", "users:write")
	rbac.DefineRole("tenant-B", "admin", "billing:write")

	tenantA := &tenant.Tenant{ID: "tenant-A", Roles: []tenant.Role{"admin"}}
	tenantB := &tenant.Tenant{ID: "tenant-B", Roles: []tenant.Role{"admin"}}

	assert.True(t, rbac.Can(tenantA, "users:write"))
	assert.False(t, rbac.Can(tenantA, "billing:write"))

	assert.True(t, rbac.Can(tenantB, "billing:write"))
	assert.False(t, rbac.Can(tenantB, "users:write"))
}
