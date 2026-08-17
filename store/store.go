package store

import (
	"context"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// Store est la source de vérité pour l'état des tenants.
type Store interface {
	/**
	 * Get retourne le tenant correspondant à l'ID donné.
		Peut être servi depuis un cache local selon l'implémentation.
	 */
	Get(ctx context.Context, id tenant.TenantID) (*tenant.Tenant, error)

	/**
	 * IsBanned vérifie si un tenant est banni.
		Doit TOUJOURS interroger la source de vérité, jamais un cache,
		pour garantir un bannissement immédiat (voir cahier des charges, section 6).
	 */
	IsBanned(ctx context.Context, id tenant.TenantID) (bool, error)
}