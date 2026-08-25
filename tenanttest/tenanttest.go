package tenanttest

import (
	"context"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"
)

// WithFakeTenant returns a context.Context containing a minimal fake
// tenant (no roles), to test application code that calls
// tenantctx.FromContext(...) without needing a real Resolver or a
// real Store.
func WithFakeTenant(ctx context.Context, id tenant.TenantID, state tenant.State) context.Context {
	return WithFakeTenantFull(ctx, &tenant.Tenant{
		ID:    id,
		State: state,
	})
}

// WithFakeTenantFull returns a context.Context containing the given
// tenant as-is, for test scenarios requiring more control (e.g.
// RBAC roles).
func WithFakeTenantFull(ctx context.Context, t *tenant.Tenant) context.Context {
	return tenantctx.WithTenant(ctx, t)
}
