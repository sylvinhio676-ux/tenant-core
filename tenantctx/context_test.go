package tenantctx

import (
	"context"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

func TestWithTenant_And_FromContext(t *testing.T) {
	// Given : un tenant simple
	tenantA := &tenant.Tenant{
		ID:    "tenant-A",
		State: tenant.Active,
	}

	// When : on l'injecte dans un contexte
	ctx := WithTenant(context.Background(), tenantA)
	got := FromContext(ctx)

	// Then : on doit le retrouver identique
	assert.Equal(t, tenantA.ID, got.ID)
	assert.Equal(t, tenantA.State, got.State)
}


func TestTenantContext_Isolated(t *testing.T) {
	// Given : deux tenants distincts
	tenantA := &tenant.Tenant{ID: "tenant-A", State: tenant.Active}
	tenantB := &tenant.Tenant{ID: "tenant-B", State: tenant.Active}

	// When : chaque tenant est placé dans son propre contexte
	ctxA := WithTenant(context.Background(), tenantA)
	ctxB := WithTenant(context.Background(), tenantB)

	gotA := FromContext(ctxA)
	gotB := FromContext(ctxB)

	// Then : chaque contexte retourne le bon tenant
	assert.Equal(t, tenant.TenantID("tenant-A"), gotA.ID)
	assert.Equal(t, tenant.TenantID("tenant-B"), gotB.ID)

	// On modifie via CE QUE LE CONTEXTE A RETOURNÉ, pas la variable d'origine
	gotA.Roles = []string{"admin"}

	// gotB (récupéré depuis ctxB) ne doit jamais voir cette modification
	assert.Nil(t, gotB.Roles)
}