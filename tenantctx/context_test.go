package tenantctx

import (
	"context"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

func TestWithTenant_And_FromContext(t *testing.T) {
	// Given: a simple tenant
	tenantA := &tenant.Tenant{
		ID:    "tenant-A",
		State: tenant.Active,
	}

	// When: it is injected into a context
	ctx := WithTenant(context.Background(), tenantA)
	got := FromContext(ctx)

	// Then: it must be retrieved unchanged
	assert.Equal(t, tenantA.ID, got.ID)
	assert.Equal(t, tenantA.State, got.State)
}


func TestTenantContext_Isolated(t *testing.T) {
	// Given: two distinct tenants
	tenantA := &tenant.Tenant{ID: "tenant-A", State: tenant.Active}
	tenantB := &tenant.Tenant{ID: "tenant-B", State: tenant.Active}

	// When: each tenant is placed in its own context
	ctxA := WithTenant(context.Background(), tenantA)
	ctxB := WithTenant(context.Background(), tenantB)

	gotA := FromContext(ctxA)
	gotB := FromContext(ctxB)

	// Then: each context returns the correct tenant
	assert.Equal(t, tenant.TenantID("tenant-A"), gotA.ID)
	assert.Equal(t, tenant.TenantID("tenant-B"), gotB.ID)

	// Mutate via WHAT THE CONTEXT RETURNED, not the original variable
	gotA.Roles = []string{"admin"}

	// gotB (retrieved from ctxB) must never see this change
	assert.Nil(t, gotB.Roles)
}
