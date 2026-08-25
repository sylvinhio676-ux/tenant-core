package admin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEventBus is a minimal eventbus.EventBus whose Publish behavior is
// scripted: it fails for the first failUntil calls, then succeeds.
// Subscribe is unused by PublishRetryQueue and implemented as a no-op
// only to satisfy the interface.
type fakeEventBus struct {
	mu        sync.Mutex
	failUntil int
	calls     int
}

func (f *fakeEventBus) Publish(ctx context.Context, event eventbus.TenantEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failUntil {
		return errors.New("publish failed")
	}
	return nil
}

func (f *fakeEventBus) Subscribe(handler func(eventbus.TenantEvent)) error {
	return nil
}

func (f *fakeEventBus) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestPublishRetryQueue_SucceedsOnFirstRetry(t *testing.T) {
	bus := &fakeEventBus{failUntil: 1} // first Publish call fails, second succeeds
	q := NewPublishRetryQueue(bus)
	q.baseBackoff = 10 * time.Millisecond // keep the test fast

	q.Enqueue(eventbus.TenantEvent{TenantID: "tenant-A", State: tenant.Banned, Timestamp: time.Now()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)
	defer q.Stop()

	require.Eventually(t, func() bool {
		return q.PendingCount() == 0
	}, 3*time.Second, 20*time.Millisecond, "event should eventually be published and removed from the queue")

	assert.Equal(t, 2, bus.callCount())
}

func TestPublishRetryQueue_GivesUpAfterMaxRetries(t *testing.T) {
	bus := &fakeEventBus{failUntil: 1_000_000} // always fails
	q := NewPublishRetryQueue(bus)
	q.baseBackoff = 10 * time.Millisecond // keep the test fast

	q.Enqueue(eventbus.TenantEvent{TenantID: "tenant-A", State: tenant.Banned, Timestamp: time.Now()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)
	defer q.Stop()

	// The event must eventually be abandoned once maxRetries is exhausted.
	require.Eventually(t, func() bool {
		return q.PendingCount() == 0
	}, 5*time.Second, 20*time.Millisecond, "event should eventually be dropped from the queue")

	// 1 initial attempt + defaultMaxRetries retries = 6 total Publish calls.
	assert.Equal(t, defaultMaxRetries+1, bus.callCount())
}

func TestPublishRetryQueue_DropsOldestWhenFull(t *testing.T) {
	bus := &fakeEventBus{failUntil: 1_000_000}
	q := NewPublishRetryQueue(bus)
	q.maxSize = 2

	// Enqueue directly, without starting the worker, so no publish
	// attempt happens between these calls.
	q.Enqueue(eventbus.TenantEvent{TenantID: "tenant-A", State: tenant.Banned, Timestamp: time.Now()})
	q.Enqueue(eventbus.TenantEvent{TenantID: "tenant-B", State: tenant.Banned, Timestamp: time.Now()})
	q.Enqueue(eventbus.TenantEvent{TenantID: "tenant-C", State: tenant.Banned, Timestamp: time.Now()})

	require.Equal(t, 2, q.PendingCount())

	q.mu.Lock()
	defer q.mu.Unlock()
	assert.Equal(t, tenant.TenantID("tenant-B"), q.pending[0].event.TenantID)
	assert.Equal(t, tenant.TenantID("tenant-C"), q.pending[1].event.TenantID)
}

func TestPublishRetryQueue_StopIsClean(t *testing.T) {
	bus := &fakeEventBus{failUntil: 1_000_000}
	q := NewPublishRetryQueue(bus)
	q.baseBackoff = 10 * time.Millisecond

	q.Enqueue(eventbus.TenantEvent{TenantID: "tenant-A", State: tenant.Banned, Timestamp: time.Now()})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	q.Start(ctx)

	stopped := make(chan struct{})
	go func() {
		q.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Stop() returned cleanly: no deadlock.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return in time — possible deadlock")
	}
}

func TestService_EnqueuesOnPublishFailure(t *testing.T) {
	store := &fakeAdminStore{}
	bus := &fakeEventBus{failUntil: 1_000_000} // Publish always fails
	retryQueue := NewPublishRetryQueue(bus)
	// The worker is intentionally never started here: this test only
	// checks that transition() enqueues on failure, not the retry
	// mechanics themselves (covered by the tests above).

	service := NewAdminService(store, bus, WithPublishRetryQueue(retryQueue))

	err := service.Ban(context.Background(), "tenant-A")

	// The original Publish error must still propagate to the caller.
	require.Error(t, err)

	// The event must have been handed off to the retry queue.
	assert.Equal(t, 1, retryQueue.PendingCount())
}
