package redis

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"

	"github.com/sylvinhio676-ux/tenant-core/eventbus"

	goredis "github.com/redis/go-redis/v9"
)

// ErrStopped is returned by Subscribe once Stop has been called. A
// stopped RedisEventBus cannot be reused.
var ErrStopped = errors.New("eventbus/redis: bus is stopped")

// RedisEventBus is an implementation of eventbus.EventBus that propagates
// events via Redis Pub/Sub, allowing propagation across multiple
// server instances — unlike eventbus.MemoryEventBus,
// which only works in a single instance.
//
// Network resilience: reconnection and resubscription on transient
// network failures are handled natively by go-redis's *redis.PubSub —
// not by this type. go-redis automatically reconnects and re-issues the
// SUBSCRIBE command on connection errors, and runs a periodic
// health-check ping (every 3s by default) to detect silent disconnects.
// The Go channel returned by pubsub.Channel() (used internally by
// Subscribe) never closes on its own because of a network error; it only
// closes when Close is called on that PubSub, which is exactly what Stop
// does. In other words: don't reimplement backoff/reconnect logic on top
// of this type, it would duplicate what go-redis already does.
type RedisEventBus struct {
	client  *goredis.Client
	channel string

	mu      sync.Mutex
	pubsubs []*goredis.PubSub
	stopped bool
}

// New creates a RedisEventBus using the given client and Redis channel.
func New(client *goredis.Client, channel string) *RedisEventBus {
	return &RedisEventBus{client: client, channel: channel}
}

func (b *RedisEventBus) Publish(ctx context.Context, event eventbus.TenantEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return b.client.Publish(ctx, b.channel, data).Err()
}

func (b *RedisEventBus) Subscribe(handler func(eventbus.TenantEvent)) error {
	pubsub := b.client.Subscribe(context.Background(), b.channel)

	// Receive() blocks until the subscription is confirmed, or returns
	// an error if Redis is unreachable — unlike Subscribe()
	// which guarantees nothing synchronously.
	if _, err := pubsub.Receive(context.Background()); err != nil {
		_ = pubsub.Close() // avoid leaking the underlying connection on failure
		return err
	}

	// Register the subscription only if Stop hasn't already run. Receive
	// above can block for a while (a slow/degraded Redis), so Stop() may
	// complete entirely — closing every pubsub it knew about — before this
	// Subscribe() call reaches this point. Without this check, such a
	// subscription would never make it into b.pubsubs and would keep
	// running forever, un-closeable, after the bus was supposedly stopped.
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		_ = pubsub.Close()
		return ErrStopped
	}
	b.pubsubs = append(b.pubsubs, pubsub)
	b.mu.Unlock()

	go func() {
		// This loop only ends when pubsub.Channel() closes, which only
		// happens once pubsub.Close() is called (by Stop). A transient
		// network error never closes this channel: go-redis reconnects
		// and resubscribes internally and simply keeps delivering
		// messages once the connection is restored.
		for msg := range pubsub.Channel() {
			var event eventbus.TenantEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				log.Printf("eventbus/redis: failed to unmarshal event: %v", err)
				continue
			}

			go safeCall(handler, event)
		}
	}()

	return nil
}

// Stop closes every subscription created via Subscribe, which ends their
// background goroutines, and marks the bus as stopped: any Subscribe call
// still in flight (blocked in Receive against a slow/degraded Redis) will
// notice this once it completes and close its own subscription instead of
// registering it — see the check in Subscribe. Once stopped, a
// RedisEventBus cannot be reused; Subscribe will return ErrStopped.
//
// Stop is safe to call multiple times, and safe to call even if Subscribe
// was never called.
//
// This is unrelated to network resilience — see the type-level doc
// comment: go-redis already reconnects and resubscribes on its own after
// a transient failure. Stop is purely for an intentional, clean shutdown
// of this RedisEventBus (e.g. when the application terminates).
func (b *RedisEventBus) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stopped = true
	for _, pubsub := range b.pubsubs {
		if err := pubsub.Close(); err != nil {
			log.Printf("eventbus/redis: error closing subscription: %v", err)
		}
	}
	b.pubsubs = nil
}

// safeCall runs a handler while recovering from any panic, so that
// a failing handler never affects other subscribers nor the
// process as a whole — same principle as MemoryEventBus.
func safeCall(handler func(eventbus.TenantEvent), event eventbus.TenantEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("eventbus/redis: handler panicked: %v", r)
		}
	}()
	handler(event)
}
