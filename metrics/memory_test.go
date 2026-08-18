package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

func TestMemoryMetrics_Snapshot(t *testing.T) {
	metrics := New()
	ctx := context.Background()
	tenantID := tenant.TenantID("tenant-A")

	// Plusieurs requêtes
	metrics.IncRequests(ctx, tenantID)
	metrics.IncRequests(ctx, tenantID)
	metrics.IncRequests(ctx, tenantID)

	// Une erreur
	metrics.IncErrors(ctx, tenantID)

	// Trois latences : 10ms, 20ms, 30ms
	metrics.ObserveLatency(ctx, tenantID, 10*time.Millisecond)
	metrics.ObserveLatency(ctx, tenantID, 20*time.Millisecond)
	metrics.ObserveLatency(ctx, tenantID, 30*time.Millisecond)

	// On récupère le snapshot
	snapshot := metrics.Snapshot(tenantID)

	// Then
	assert.Equal(t, int64(3), snapshot.Requests)
	assert.Equal(t, int64(1), snapshot.Errors)
	assert.Equal(t, 20*time.Millisecond, snapshot.AverageLatency)
}

func TestMemoryMetrics_ConcurrentIncrements(t *testing.T) {
	metrics := New()
	ctx := context.Background()
	tenantID := tenant.TenantID("tenant-A")

	const goroutines = 100
	const incrementsPerGoroutine = 100

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				metrics.IncRequests(ctx, tenantID)
			}
		}()
	}
	wg.Wait()

	snapshot := metrics.Snapshot(tenantID)
	expected := int64(goroutines * incrementsPerGoroutine)
	assert.Equal(t, expected, snapshot.Requests)
}