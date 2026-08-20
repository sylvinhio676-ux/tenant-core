package tenanttest

import (
	"context"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"
)

// WithFakeTenant retourne un context.Context contenant un tenant fictif
// minimal (sans rôles), pour tester du code applicatif qui appelle
// tenantctx.FromContext(...) sans avoir besoin d'un vrai Resolver ni d'un
// vrai Store.
func WithFakeTenant(ctx context.Context, id tenant.TenantID, state tenant.State) context.Context {
	return WithFakeTenantFull(ctx, &tenant.Tenant{
		ID:    id,
		State: state,
	})
}

// WithFakeTenantFull retourne un context.Context contenant le tenant donné
// tel quel, pour les scénarios de test nécessitant plus de contrôle (ex:
// rôles RBAC).
func WithFakeTenantFull(ctx context.Context, t *tenant.Tenant) context.Context {
	return tenantctx.WithTenant(ctx, t)
}