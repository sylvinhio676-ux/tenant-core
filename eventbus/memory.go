package eventbus

import (
	"context"
	"log"
	"sync"
)

/**
 * MemoryEventBus is an in-process implementation of EventBus.
	It only works within a single server instance — for distributed
	(multi-instance) behavior, see eventbus/redis (optional submodule,
	see spec section 6/11).
*/
type MemoryEventBus struct {
	mu       sync.RWMutex
	handlers []func(TenantEvent)
}

// NewMemoryEventBus creates an in-memory EventBus.
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
 * safeCall runs a handler while recovering from any panic,
	so that a failing handler never affects other subscribers
	nor the process as a whole.
 */
func safeCall(h func(TenantEvent), event TenantEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("eventbus: handler panicked: %v", r)
		}
	}()
	h(event)
}
