package rbac

import (
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

func TestRBAC_Can(t *testing.T) {
	rbac := New()

	// Given : un rôle "admin" avec plusieurs permissions
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

	// Then : le tenant possède la permission
	assert.True(t, rbac.Can(tenantA, "users:write"))

	// And : le tenant ne possède pas une permission inexistante
	assert.False(t, rbac.Can(tenantA, "users:delete"))
}

func TestRBAC_UnknownTenantOrRole(t *testing.T) {
	rbac := New()

	// Tenant-A possède le rôle admin
	rbac.DefineRole(
		"tenant-A",
		"admin",
		[]string{"users:read"},
	)

	// Tenant-B n'a aucune définition RBAC
	tenantB := &tenant.Tenant{
		ID:    "tenant-B",
		Roles: []string{"admin"},
	}

	// Le tenant n'existe pas dans les définitions
	assert.False(t, rbac.Can(tenantB, "users:read"))

	// Tenant-A possède un rôle qui n'existe pas
	tenantA := &tenant.Tenant{
		ID:    "tenant-A",
		Roles: []string{"viewer"},
	}

	assert.False(t, rbac.Can(tenantA, "users:read"))
}