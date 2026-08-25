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
		[]string{
			"users:read",
			"users:write",
		},
	)

	tenantA := &tenant.Tenant{
		ID:    "tenant-A",
		Roles: []string{"admin"},
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
		[]string{"users:read"},
	)

	// Tenant-B has no RBAC definitions
	tenantB := &tenant.Tenant{
		ID:    "tenant-B",
		Roles: []string{"admin"},
	}

	// The tenant does not exist in the definitions
	assert.False(t, rbac.Can(tenantB, "users:read"))

	// Tenant-A has a role that doesn't exist
	tenantA := &tenant.Tenant{
		ID:    "tenant-A",
		Roles: []string{"viewer"},
	}

	assert.False(t, rbac.Can(tenantA, "users:read"))
}
