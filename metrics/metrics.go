package metrics

import (
	"context"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// MetricsCollector captures metrics broken down by tenant, without
// imposing a specific backend (Prometheus, StatsD, etc.) — see spec,
// functional requirement #5.
type MetricsCollector interface {
	IncRequests(ctx context.Context, tenantID tenant.TenantID)
	ObserveLatency(ctx context.Context, tenantID tenant.TenantID, duration time.Duration)
	IncErrors(ctx context.Context, tenantID tenant.TenantID)
}
