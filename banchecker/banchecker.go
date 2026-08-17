package banchecker

import (
	"context"
	"sync"
	"time"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/eventbus"
	"github.com/sylvinhio676-ux/tenant-core/store"
)

/**
 * banEntry représente la dernière information connue sur l'état de
	bannissement d'un tenant, avec l'horodatage de cette information —
	nécessaire pour ne jamais laisser une donnée périmée (ex: un snapshot
	initial lancé avant un événement récent) écraser une donnée plus récente.
 */
type banEntry struct {
	banned    bool
	updatedAt time.Time
}

/**
 * BanChecker maintient en mémoire la liste des tenants actuellement bannis,
	mise à jour par événement plutôt que par lecture systématique de la
	source de vérité — voir cahier des charges section 11 (Stratégie de
	performance).
 */
type BanChecker struct {
	banned sync.Map // TenantID -> banEntry
}

/**
 * New crée un BanChecker et s'abonne immédiatement à l'EventBus donné.
	Important : Subscribe doit toujours être appelé AVANT LoadInitialBannedList,
	pour ne jamais manquer un événement publié pendant le chargement du
	snapshot initial.
 */
func New(bus eventbus.EventBus) *BanChecker {
	bc := &BanChecker{}
	bus.Subscribe(bc.handleEvent)
	return bc
}

/**
 * apply met à jour l'état d'un tenant uniquement si l'information fournie
	est plus récente (ou aussi récente) que celle déjà connue. Ça garantit
	qu'un snapshot périmé ne peut jamais écraser un événement plus récent,
	quel que soit l'ordre réel d'exécution des goroutines.
 */
func (bc *BanChecker) apply(id tenant.TenantID, banned bool, at time.Time) {
	existing, loaded := bc.banned.Load(id)
	if loaded {
		e := existing.(banEntry)
		if e.updatedAt.After(at) {
			// La donnée déjà stockée est plus récente que celle qu'on
			// essaie d'écrire : on l'ignore pour ne jamais régresser.
			return
		}
	}
	bc.banned.Store(id, banEntry{banned: banned, updatedAt: at})
}

func (bc *BanChecker) handleEvent(event eventbus.TenantEvent) {
	bc.apply(event.TenantID, event.State == tenant.Banned, event.Timestamp)
}

// IsBanned vérifie en mémoire (lecture pure, aucun accès réseau) si un
// tenant est actuellement banni.
func (bc *BanChecker) IsBanned(id tenant.TenantID) bool {
	v, ok := bc.banned.Load(id)
	if !ok {
		return false
	}
	return v.(banEntry).banned
}

/**
 * LoadInitialBannedList charge, une seule fois au démarrage, l'état des
	tenants donnés depuis la source de vérité — nécessaire en environnement
	multi-instance où une nouvelle instance n'a pas l'historique des
	événements passés (cahier des charges section 6/11). Doit être appelée
	après Subscribe (voir New).
 */
func (bc *BanChecker) LoadInitialBannedList(ctx context.Context, source store.Store, ids []tenant.TenantID) error {
	snapshotTime := time.Now()
	for _, id := range ids {
		banned, err := source.IsBanned(ctx, id)
		if err != nil {
			return err
		}
		bc.apply(id, banned, snapshotTime)
	}
	return nil
}