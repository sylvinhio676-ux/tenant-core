package rbac

import (
	"sync"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// RBAC gère les permissions associées aux rôles, définies indépendamment
// pour chaque tenant (voir cahier des charges, besoin fonctionnel #7).
type RBAC struct {
	mu sync.RWMutex
	// definitions[tenantID][role][permission] = présence (set)
	definitions map[tenant.TenantID]map[string]map[string]struct{}
}

// New crée un RBAC vide.
func New() *RBAC {
	return &RBAC{
		definitions: make(map[tenant.TenantID]map[string]map[string]struct{}),
	}
}

// DefineRole associe une liste de permissions à un rôle, pour un tenant donné.
func (r *RBAC) DefineRole(tenantID tenant.TenantID, role string, permissions []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.definitions[tenantID] == nil {
		r.definitions[tenantID] = make(map[string]map[string]struct{})
	}

	permSet := make(map[string]struct{}, len(permissions))
	for _, p := range permissions {
		permSet[p] = struct{}{}
	}
	r.definitions[tenantID][role] = permSet
}

// Can vérifie si le tenant donné, via l'un de ses rôles, possède la
// permission demandée.
func (r *RBAC) Can(t *tenant.Tenant, permission string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tenantRoles, ok := r.definitions[t.ID]
	if !ok {
		return false
	}

	for _, role := range t.Roles {
		if permSet, ok := tenantRoles[role]; ok {
			if _, has := permSet[permission]; has {
				return true
			}
		}
	}
	return false
}