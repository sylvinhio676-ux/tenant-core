package ratelimit

import (
	"fmt"
	"sync"
	"testing"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"golang.org/x/time/rate"
)

//Benchmark A — hot path
func BenchmarkRateLimiter_Allow_Warm(b *testing.B) {
	tenantCounts := []int{1, 10, 100, 1000}

	for _, count := range tenantCounts {
		b.Run(fmt.Sprintf("%d_tenants", count), func(b *testing.B) {
			rl := NewTenantRateLimiter(func(t *tenant.Tenant) rate.Limit {
				return rate.Limit(1e9) // very large, to never block in this benchmark
			}, 1e9)

			tenants := make([]*tenant.Tenant, count)
			for i := 0; i < count; i++ {
				tenants[i] = &tenant.Tenant{ID: tenant.TenantID(fmt.Sprintf("tenant-%d", i))}
				rl.Allow(tenants[i]) // pre-create the limiter for each tenant
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				rl.Allow(tenants[i%count])
			}
		})
	}
}

//Benchmark B — concurrent initialization, same tenant
func BenchmarkRateLimiter_ConcurrentInit_SameTenant(b *testing.B) {
	goroutineCounts := []int{1, 2, 4, 8, 16, 32}

	for _, n := range goroutineCounts {
		b.Run(fmt.Sprintf("%d_goroutines", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				rl := NewTenantRateLimiter(func(t *tenant.Tenant) rate.Limit {
					return rate.Limit(1e9)
				}, 1e9)
				tn := &tenant.Tenant{ID: "tenant-A"}

				var wg sync.WaitGroup
				for g := 0; g < n; g++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						rl.Allow(tn)
					}()
				}
				wg.Wait()
			}
		})
	}
}

//Benchmark C — parallel initialization of different tenants
func BenchmarkRateLimiter_ConcurrentInit_DifferentTenants(b *testing.B) {
	tenantCounts := []int{10, 100, 1000}

	for _, count := range tenantCounts {
		b.Run(fmt.Sprintf("%d_tenants", count), func(b *testing.B) {
			tenants := make([]*tenant.Tenant, count)
			for i := 0; i < count; i++ {
				tenants[i] = &tenant.Tenant{ID: tenant.TenantID(fmt.Sprintf("tenant-%d", i))}
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				rl := NewTenantRateLimiter(func(t *tenant.Tenant) rate.Limit {
					return rate.Limit(1e9)
				}, 1e9)

				var wg sync.WaitGroup
				for _, tn := range tenants {
					wg.Add(1)
					go func(t *tenant.Tenant) {
						defer wg.Done()
						rl.Allow(t)
					}(tn)
				}
				wg.Wait()
			}
		})
	}
}
