package tenantctx

import (
	"context"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

type contextKey int

const tenantContextKey contextKey = 0

// WithTenant retourne un nouveau contexte contenant le tenant donné.
func WithTenant(ctx context.Context, t *tenant.Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey, t)
}

// FromContext extrait le tenant du contexte. Retourne nil si absent.
func FromContext(ctx context.Context) *tenant.Tenant {
	if ctx == nil {
		return nil
	}
	t, ok := ctx.Value(tenantContextKey).(*tenant.Tenant)
	if !ok {
		return nil
	}
	return t
}