package redis

import (
	"context"
	"testing"
	"time"

	"github.com/sylvinhio676-ux/tenant-core/eventbus"
	tenant "github.com/sylvinhio676-ux/tenant-core"

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
