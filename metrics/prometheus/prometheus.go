// Package prometheus provides a Prometheus-backed implementation of
// metrics.MetricsCollector.
package prometheus

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/metrics"
)

var _ metrics.MetricsCollector = (*PrometheusMetrics)(nil)

// PrometheusMetrics is a Prometheus-backed implementation of
// metrics.MetricsCollector.
//
// Cardinality note — read before enabling WithTenantLabels: by default,
// metrics are GLOBAL. They carry no tenant_id label at all, regardless of
// which tenant a given call is for. This is a deliberate default, not an
// oversight: labeling every metric by tenant_id creates one Prometheus
// time series per tenant per metric, and a platform with many thousands
// of tenants can turn this into a cardinality explosion that degrades or
// crashes a Prometheus server (see docs/ARCHITECTURE.md §8.15). The
// tenant_id label is only added when WithTenantLabels is passed to New —
// an explicit, informed choice, never the default.
type PrometheusMetrics struct {
	requests     *prometheus.CounterVec
	errors       *prometheus.CounterVec
	latency      *prometheus.HistogramVec
	withTenantID bool
}

// Option configures a PrometheusMetrics at creation time.
type Option func(*PrometheusMetrics)

// WithTenantLabels adds a tenant_id label to every metric, using the
// real TenantID passed to IncRequests/IncErrors/ObserveLatency.
//
// WARNING: high cardinality. Each distinct tenant becomes its own
// Prometheus time series for every metric this type exposes. On a
// platform with thousands of tenants, this can produce a cardinality
// explosion that degrades — or crashes — a Prometheus server. Do not
// enable this without having evaluated the number of tenants against
// your Prometheus infrastructure's capacity. Without this option (the
// default), metrics stay global and carry no tenant_id label at all.
func WithTenantLabels() Option {
	return func(m *PrometheusMetrics) {
		m.withTenantID = true
	}
}

// New creates a PrometheusMetrics and registers its metrics on
// registerer. Passing a custom *prometheus.Registry (rather than
// prometheus.DefaultRegisterer) keeps registration under the caller's
// control and makes this type easy to test in isolation.
func New(registerer prometheus.Registerer, opts ...Option) *PrometheusMetrics {
	m := &PrometheusMetrics{}
	for _, opt := range opts {
		opt(m)
	}

	var labels []string
	if m.withTenantID {
		labels = []string{"tenant_id"}
	}

	m.requests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tenant_requests_total",
		Help: "Total number of requests processed.",
	}, labels)

	m.errors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tenant_errors_total",
		Help: "Total number of errors encountered.",
	}, labels)

	m.latency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "tenant_request_latency_seconds",
		Help: "Request latency distribution, in seconds.",
	}, labels)

	registerer.MustRegister(m.requests, m.errors, m.latency)

	return m
}

// labelValues returns the label values to use for a call concerning
// tenantID: none in global mode, or the single tenant_id value in
// per-tenant mode.
func (m *PrometheusMetrics) labelValues(tenantID tenant.TenantID) []string {
	if !m.withTenantID {
		return nil
	}
	return []string{string(tenantID)}
}

func (m *PrometheusMetrics) IncRequests(ctx context.Context, tenantID tenant.TenantID) {
	m.requests.WithLabelValues(m.labelValues(tenantID)...).Inc()
}

func (m *PrometheusMetrics) IncErrors(ctx context.Context, tenantID tenant.TenantID) {
	m.errors.WithLabelValues(m.labelValues(tenantID)...).Inc()
}

func (m *PrometheusMetrics) ObserveLatency(ctx context.Context, tenantID tenant.TenantID, duration time.Duration) {
	m.latency.WithLabelValues(m.labelValues(tenantID)...).Observe(duration.Seconds())
}
