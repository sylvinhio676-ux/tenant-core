package admin

import (
	"context"
	"log"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
)

// Service orchestre les opérations d'administration des tenants,
// garantissant que tout changement d'état est toujours accompagné de la
// publication de l'événement correspondant.
//
// Limite connue : SetState et Publish ne sont pas atomiques entre eux (ce
// sont deux systèmes distincts). L'ordre SetState → Publish garantit qu'on
// ne publie jamais un événement pour un état qui n'a pas réellement été
// appliqué au Store — mais si Publish échoue après un SetState réussi,
// l'événement peut être perdu jusqu'à resynchronisation manuelle ou via un
// futur mécanisme de livraison durable (pattern Outbox).
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

// Ban désactive définitivement un tenant pour fraude/abus, et propage
// immédiatement le changement à toutes les instances abonnées à l'EventBus.
func (s *Service) Ban(ctx context.Context, id tenant.TenantID) error {
	return s.transition(ctx, id, tenant.Banned)
}

// Disable désactive un tenant (ex: fin d'abonnement).
func (s *Service) Disable(ctx context.Context, id tenant.TenantID) error {
	return s.transition(ctx, id, tenant.Disabled)
}

// Activate réactive un tenant (unban ou fin de désactivation).
func (s *Service) Activate(ctx context.Context, id tenant.TenantID) error {
	return s.transition(ctx, id, tenant.Active)
}