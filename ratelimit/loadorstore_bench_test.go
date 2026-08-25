package ratelimit

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/time/rate"
)

// This benchmark isolates the sync.Map.LoadOrStore primitive exactly as
// used in getLimiter(), to experimentally verify how many
// *rate.Limiter candidates are actually built under contention —
// without modifying TenantRateLimiter itself.
func BenchmarkLoadOrStore_CandidateCount(b *testing.B) {
	goroutineCounts := []int{1, 2, 4, 8, 16, 32}

	for _, n := range goroutineCounts {
		b.Run(fmt.Sprintf("%d_goroutines", n), func(b *testing.B) {
			var candidates atomic.Int64

			for i := 0; i < b.N; i++ {
				var m sync.Map

				var wg sync.WaitGroup
				for g := 0; g < n; g++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						candidate := rate.NewLimiter(rate.Limit(1e9), int(1e9))
						candidates.Add(1)
						m.LoadOrStore("tenant-A", candidate)
					}()
				}
				wg.Wait()
			}

			b.ReportMetric(float64(candidates.Load())/float64(b.N), "candidates/op")
		})
	}
}
