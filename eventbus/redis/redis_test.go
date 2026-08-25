package redis

import (
	"context"
	"testing"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisEventBus_PublishAndSubscribe(t *testing.T) {
	// Given: an in-memory miniredis, no real Redis needed
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	bus := New(client, "tenant-events")

	events := make(chan eventbus.TenantEvent, 1)
	err = bus.Subscribe(func(event eventbus.TenantEvent) {
		events <- event
	})
	require.NoError(t, err)

	// When: an event is published
	sent := eventbus.TenantEvent{
		TenantID:  "tenant-A",
		State:     tenant.Banned,
		Timestamp: time.Now(),
	}
	err = bus.Publish(context.Background(), sent)
	require.NoError(t, err)

	// Then: the event must be received, correctly deserialized
	select {
	case received := <-events:
		assert.Equal(t, sent.TenantID, received.TenantID)
		assert.Equal(t, sent.State, received.State)
		assert.WithinDuration(t, sent.Timestamp, received.Timestamp, time.Second)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: expected event was not received")
	}
}

func TestRedisEventBus_SubscribeFailsWhenRedisUnavailable(t *testing.T) {
	// Given: a client pointing to an address with no Redis server
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	bus := New(client, "tenant-events")

	err := bus.Subscribe(func(event eventbus.TenantEvent) {})

	// Then: Subscribe() must fail immediately (fail-fast), not
	// silently.
	assert.Error(t, err)
}

func TestRedisEventBus_Stop_StopsDeliveringMessages(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	bus := New(client, "tenant-events")

	events := make(chan eventbus.TenantEvent, 2)
	err = bus.Subscribe(func(event eventbus.TenantEvent) {
		events <- event
	})
	require.NoError(t, err)

	// Sanity check: delivery works before Stop.
	err = bus.Publish(context.Background(), eventbus.TenantEvent{
		TenantID: "tenant-A", State: tenant.Banned, Timestamp: time.Now(),
	})
	require.NoError(t, err)

	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: expected event was not received before Stop")
	}

	bus.Stop()

	// After Stop, a new publish must not reach the now-closed subscription.
	err = bus.Publish(context.Background(), eventbus.TenantEvent{
		TenantID: "tenant-B", State: tenant.Banned, Timestamp: time.Now(),
	})
	require.NoError(t, err)

	select {
	case event := <-events:
		t.Fatalf("unexpected event received after Stop: tenant=%s", event.TenantID)
	case <-time.After(200 * time.Millisecond):
		// No event received: expected, the subscription was closed by Stop.
	}
}

func TestRedisEventBus_Stop_IsIdempotent(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	bus := New(client, "tenant-events")

	err = bus.Subscribe(func(event eventbus.TenantEvent) {})
	require.NoError(t, err)

	assert.NotPanics(t, func() {
		bus.Stop()
		bus.Stop()
	})
}

func TestRedisEventBus_Subscribe_FailsAfterStop(t *testing.T) {
	// Guards against the race where Stop() finishes while a Subscribe()
	// call is still in flight (blocked in Receive against a slow Redis):
	// such a subscription must never be silently registered and left
	// running after the bus was supposedly stopped. This test exercises
	// the simple, deterministic ordering (Stop before Subscribe); the
	// same guard inside Subscribe also covers the interleaved case.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	bus := New(client, "tenant-events")

	bus.Stop()

	err = bus.Subscribe(func(event eventbus.TenantEvent) {})

	assert.ErrorIs(t, err, ErrStopped)
}

func TestRedisEventBus_Stop_SafeWithoutSubscribe(t *testing.T) {
	// A bus that never successfully subscribed to anything must still
	// tolerate Stop() being called (e.g. during generic shutdown code
	// that always calls Stop, regardless of whether Subscribe ran).
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	bus := New(client, "tenant-events")

	assert.NotPanics(t, func() {
		bus.Stop()
	})
}
