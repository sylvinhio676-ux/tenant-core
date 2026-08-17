package eventbus

import (
	"context"
	"log"
	"sync"
)

/**
 * MemoryEventBus est une implémentation in-process de EventBus.
	Elle ne fonctionne qu'au sein d'une seule instance du serveur — pour un
	comportement distribué (multi-instance), voir eventbus/redis (sous-module
	optionnel, voir cahier des charges section 6/11).
*/
type MemoryEventBus struct {
	mu       sync.RWMutex
	handlers []func(TenantEvent)
}

// NewMemoryEventBus crée un EventBus in-memory.
func NewMemoryEventBus() *MemoryEventBus {
	return &MemoryEventBus{}
}

func (b *MemoryEventBus) Subscribe(handler func(TenantEvent)) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, handler)
	return nil
}

func (b *MemoryEventBus) Publish(ctx context.Context, event TenantEvent) error {
	b.mu.RLock()
	handlers := make([]func(TenantEvent), len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()

	for _, h := range handlers {
		go safeCall(h, event)
	}
	return nil
}

/**
 * safeCall exécute un handler en récupérant une éventuelle panique,
	pour qu'un handler défaillant n'affecte jamais les autres abonnés
	ni le processus dans son ensemble.
 */
func safeCall(h func(TenantEvent), event TenantEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("eventbus: handler panicked: %v", r)
		}
	}()
	h(event)
}