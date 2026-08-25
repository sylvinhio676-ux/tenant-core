package admin

import (
	"context"
	"log"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
)

// Service orchestrates tenant administration operations,
// guaranteeing that every state change is always accompanied by the
// publication of the corresponding event.
//
// Known limitation: SetState and Publish are not atomic with each other (they
// are two distinct systems). The order SetState → Publish guarantees that we
// never publish an event for a state that was not actually
// applied to the Store — but if Publish fails after a successful SetState,
// the event may be lost until manual resynchronization or a
// future durable-delivery mechanism (Outbox pattern).
type Service struct {
	store tenant.AdminStore
	bus   eventbus.EventBus
}

func NewAdminService(store tenant.AdminStore, bus eventbus.EventBus) *Service {
	return &Service{store: store, bus: bus}
}

func (s *Service) transition(ctx context.Context, id tenant.TenantID, state tenant.State) error {
	if err := s.store.SetState(ctx, id, state); err != nil {
		return err
	}

	event := eventbus.TenantEvent{
		TenantID:  id,
		State:     state,
		Timestamp: time.Now(),
	}

	if err := s.bus.Publish(ctx, event); err != nil {
		log.Printf(
			"ERROR tenant state changed but event publication failed: tenant_id=%s state=%s error=%v",
			id, state, err,
		)
		return err
	}

	return nil
}

// Ban permanently disables a tenant for fraud/abuse, and immediately
// propagates the change to all instances subscribed to the EventBus.
func (s *Service) Ban(ctx context.Context, id tenant.TenantID) error {
	return s.transition(ctx, id, tenant.Banned)
}

// Disable disables a tenant (e.g. end of subscription).
func (s *Service) Disable(ctx context.Context, id tenant.TenantID) error {
	return s.transition(ctx, id, tenant.Disabled)
}

// Activate reactivates a tenant (unban or end of disabling).
func (s *Service) Activate(ctx context.Context, id tenant.TenantID) error {
	return s.transition(ctx, id, tenant.Active)
}
