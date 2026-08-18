package metrics

import (
	"context"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// MetricsCollector capture des métriques ventilées par tenant, sans
// imposer de backend précis (Prometheus, StatsD, etc.) — voir cahier des
// charges besoin fonctionnel #5.
type MetricsCollector interface {
	IncRequests(ctx context.Context, tenantID tenant.TenantID)
	ObserveLatency(ctx context.Context, tenantID tenant.TenantID, duration time.Duration)
	IncErrors(ctx context.Context, tenantID tenant.TenantID)
}