package store

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/stretchr/testify/assert"
)

// countingStore is a fake Store that counts its calls and simulates latency.
type countingStore struct {
	calls int64
}

func (cs *countingStore) Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error) {
	atomic.AddInt64(&cs.calls, 1)
	time.Sleep(50 * time.Millisecond) // simulates a slow DB round-trip
	return &tenant.Tenant{ID: id, State: tenant.Active}, nil
}

func (cs *countingStore) IsBanned(ctx context.Context, id tenant.TenantID) (bool, error) {
	return false, nil
}

func TestCachedStore_DeduplicatesConcurrentCalls(t *testing.T) {
	source := &countingStore{}
	cached := NewCachedStore(source, 10*time.Second)

	var wg sync.WaitGroup

	// 20 goroutines ALL request the same tenant at the same time
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cached.Get(context.Background(), "tenant-A")
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	// Then: the source must have been called ONLY ONCE,
	// not 20 times, despite the 20 concurrent calls
	assert.Equal(t, int64(1), atomic.LoadInt64(&source.calls))
}
