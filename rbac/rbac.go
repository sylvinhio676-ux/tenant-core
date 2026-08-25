package rbac

import (
	"sync"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// RBAC manages permissions associated with roles, defined independently
// for each tenant (see spec, functional requirement #7).
type RBAC struct {
	mu sync.RWMutex
	// definitions[tenantID][role][permission] = presence (set)
	definitions map[tenant.TenantID]map[string]map[string]struct{}
}

// New creates an empty RBAC.
func New() *RBAC {
	return &RBAC{
		definitions: make(map[tenant.TenantID]map[string]map[string]struct{}),
	}
}

// DefineRole associates a list of permissions with a role, for a given tenant.
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

// Can checks whether the given tenant, via one of its roles, has the
// requested permission.
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
