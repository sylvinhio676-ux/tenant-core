package prometheus

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrometheusMetrics_GlobalMode_NoTenantLabel(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := New(registry)

	m.IncRequests(context.Background(), "tenant-A")
	m.IncRequests(context.Background(), "tenant-B")

	// Global mode: a single, label-less series accumulates both calls.
	assert.Equal(t, float64(2), testutil.ToFloat64(m.requests))

	// Verify no tenant_id label is exposed anywhere on this metric.
	metricFamilies, err := registry.Gather()
	require.NoError(t, err)

	found := false
	for _, mf := range metricFamilies {
		if mf.GetName() != "tenant_requests_total" {
			continue
		}
		found = true
		for _, metric := range mf.GetMetric() {
			for _, label := range metric.GetLabel() {
				assert.NotEqual(t, "tenant_id", label.GetName(), "global mode must not expose a tenant_id label")
			}
		}
	}
	assert.True(t, found, "tenant_requests_total metric family not found")
}

func TestPrometheusMetrics_TenantMode_WithLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := New(registry, WithTenantLabels())

	m.IncRequests(context.Background(), "tenant-A")
	m.IncRequests(context.Background(), "tenant-A")
	m.IncRequests(context.Background(), "tenant-B")

	// Each tenant must accumulate in its own, independent series.
	assert.Equal(t, float64(2), testutil.ToFloat64(m.requests.WithLabelValues("tenant-A")))
	assert.Equal(t, float64(1), testutil.ToFloat64(m.requests.WithLabelValues("tenant-B")))
}

func TestPrometheusMetrics_ObserveLatency(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := New(registry)

	m.ObserveLatency(context.Background(), "tenant-A", 50*time.Millisecond)
	m.ObserveLatency(context.Background(), "tenant-A", 150*time.Millisecond)

	metricFamilies, err := registry.Gather()
	require.NoError(t, err)

	var sampleCount uint64
	for _, mf := range metricFamilies {
		if mf.GetName() != "tenant_request_latency_seconds" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			sampleCount += metric.GetHistogram().GetSampleCount()
		}
	}

	assert.Equal(t, uint64(2), sampleCount)
}

func TestPrometheusMetrics_IncErrors(t *testing.T) {
	registry := prometheus.NewRegistry()
	m := New(registry)

	m.IncErrors(context.Background(), "tenant-A")
	m.IncErrors(context.Background(), "tenant-B")

	assert.Equal(t, float64(2), testutil.ToFloat64(m.errors))
}
