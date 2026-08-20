package redis

import (
	"context"
	"encoding/json"
	"log"

	"github.com/sylvinhio676-ux/tenant-core/eventbus"

	goredis "github.com/redis/go-redis/v9"
)

// RedisEventBus est une implémentation de eventbus.EventBus qui propage
// les événements via Redis Pub/Sub, permettant une propagation entre
// plusieurs instances du serveur — contrairement à eventbus.MemoryEventBus
// qui ne fonctionne qu'en mono-instance.
type RedisEventBus struct {
	client  *goredis.Client
	channel string
}

// New crée un RedisEventBus utilisant le client et le canal Redis donnés.
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

	// Receive() bloque jusqu'à confirmation de l'abonnement, ou retourne
	// une erreur si Redis est injoignable — contrairement à Subscribe()
	// qui ne garantit rien de façon synchrone.
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

// safeCall exécute un handler en récupérant une éventuelle panique, pour
// qu'un handler défaillant n'affecte jamais les autres abonnés ni le
// processus dans son ensemble — même principe que MemoryEventBus.
func safeCall(handler func(eventbus.TenantEvent), event eventbus.TenantEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("eventbus/redis: handler panicked: %v", r)
		}
	}()
	handler(event)
}