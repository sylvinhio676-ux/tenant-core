package redis

import (
	"context"
	"encoding/json"
	"log"

	"github.com/sylvinhio676-ux/tenant-core/eventbus"

	goredis "github.com/redis/go-redis/v9"
)

// RedisEventBus is an implementation of eventbus.EventBus that propagates
// events via Redis Pub/Sub, allowing propagation across multiple
// server instances — unlike eventbus.MemoryEventBus,
// which only works in a single instance.
type RedisEventBus struct {
	client  *goredis.Client
	channel string
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
		return err
	}

	go func() {
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
