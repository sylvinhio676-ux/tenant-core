package metrics

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

type tenantMetrics struct {
	requests     atomic.Int64
	errors       atomic.Int64
	latencySum   atomic.Int64
	latencyCount atomic.Int64
}

// MemoryMetrics is an in-memory implementation of MetricsCollector,
// useful for dev/test or simple cases that don't need Prometheus.
type MemoryMetrics struct {
	tenants sync.Map // TenantID -> *tenantMetrics
}

// New creates an empty MemoryMetrics.
func New() *MemoryMetrics {
	return &MemoryMetrics{}
}

func (m *MemoryMetrics) getOrCreate(id tenant.TenantID) *tenantMetrics {
	v, _ := m.tenants.LoadOrStore(id, &tenantMetrics{})
	return v.(*tenantMetrics)
}

func (m *MemoryMetrics) IncRequests(ctx context.Context, tenantID tenant.TenantID) {
	m.getOrCreate(tenantID).requests.Add(1)
}

func (m *MemoryMetrics) IncErrors(ctx context.Context, tenantID tenant.TenantID) {
	m.getOrCreate(tenantID).errors.Add(1)
}

func (m *MemoryMetrics) ObserveLatency(ctx context.Context, tenantID tenant.TenantID, duration time.Duration) {
	tm := m.getOrCreate(tenantID)
	tm.latencySum.Add(int64(duration))
	tm.latencyCount.Add(1)
}

// Snapshot returns a readable summary of a tenant's metrics — useful
// for tests and debugging, not meant to replace a real observability
// backend like Prometheus.
type Snapshot struct {
	Requests     int64
	Errors       int64
	AverageLatency time.Duration
}

func (m *MemoryMetrics) Snapshot(id tenant.TenantID) Snapshot {
	tm := m.getOrCreate(id)

	count := tm.latencyCount.Load()
	var avg time.Duration
	if count > 0 {
		avg = time.Duration(tm.latencySum.Load() / count)
	}

	return Snapshot{
		Requests:       tm.requests.Load(),
		Errors:         tm.errors.Load(),
		AverageLatency: avg,
	}
}
