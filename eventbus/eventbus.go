package eventbus

import (
	"context"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// TenantEvent représente un changement d'état d'un tenant.
type TenantEvent struct {
	TenantID  tenant.TenantID
	State     tenant.State
	Timestamp time.Time
}

// EventBus permet de publier et de s'abonner aux changements d'état des tenants.
type EventBus interface {
	// Publish diffuse un événement à tous les abonnés.
	Publish(ctx context.Context, event TenantEvent) error

	// Subscribe enregistre un handler appelé pour chaque événement publié.
	Subscribe(handler func(TenantEvent)) error
}