package tenantctx

import (
	"context"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

type contextKey int

const tenantContextKey contextKey = 0

// WithTenant returns a new context containing the given tenant.
func WithTenant(ctx context.Context, t *tenant.Tenant) context.Context {
	return context.WithValue(ctx, tenantContextKey, t)
}

// FromContext extracts the tenant from the context. Returns nil if absent.
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
