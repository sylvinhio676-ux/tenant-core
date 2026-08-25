package eventbus

import (
	"context"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// TenantEvent represents a tenant state change.
type TenantEvent struct {
	TenantID  tenant.TenantID
	State     tenant.State
	Timestamp time.Time
}

// EventBus allows publishing and subscribing to tenant state changes.
type EventBus interface {
	// Publish broadcasts an event to all subscribers.
	Publish(ctx context.Context, event TenantEvent) error

	// Subscribe registers a handler called for each published event.
	Subscribe(handler func(TenantEvent)) error
}
