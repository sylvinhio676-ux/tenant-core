package redis

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// BenchmarkRedisRateLimiter_Allow measures the cost of a single Allow()
// call against miniredis (an in-process fake Redis). This still goes
// through a real TCP loopback round-trip and the Lua script evaluation —
// it is NOT a pure in-memory function call, so it is not directly
// representative of a genuinely remote Redis, which adds real network
// latency on top of what's measured here.
//
// For reference, ratelimit.TenantRateLimiter's local, in-memory
// equivalent (BenchmarkRateLimiter_Allow_Warm in
// ratelimit/ratelimit_bench_test.go) runs at roughly 320-460ns per call —
// several orders of magnitude faster, since it never leaves the process.
// This benchmark exists to make that latency/precision trade-off
// concrete, not to produce a number meant to stand in for real Redis
// production latency.
func BenchmarkRedisRateLimiter_Allow(b *testing.B) {
	mr, err := miniredis.Run()
	if err != nil {
		b.Fatal(err)
	}
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	rl := New(client, func(t *tenant.Tenant) (int, time.Duration) {
		return 1_000_000_000, time.Hour // effectively unlimited, never denies during the benchmark
	})

	tn := &tenant.Tenant{ID: "tenant-A"}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		rl.Allow(tn)
	}
}
