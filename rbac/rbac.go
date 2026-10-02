package rbac

import (
	"sync"

	tenant "github.com/sylvinhio676-ux/tenant-core"
)

// Permission is a named capability that a role can grant. A dedicated named
// type rather than a plain string, for the same type-safety reason as
// tenant.TenantID: it expresses intent and stops a permission from being
// mixed up with an unrelated string at compile time. A string literal like
// "users:write" can still be passed directly wherever a Permission is
// expected, since Go implicitly converts an untyped string constant to any
// named string type.
type Permission string

// RBAC manages permissions associated with roles, defined independently
// for each tenant (see spec, functional requirement #7).
type RBAC struct {
	mu sync.RWMutex
	// definitions[tenantID][role][permission] = presence (set)
	definitions map[tenant.TenantID]map[tenant.Role]map[Permission]struct{}
}

// New creates an empty RBAC.
func New() *RBAC {
	return &RBAC{
		definitions: make(map[tenant.TenantID]map[tenant.Role]map[Permission]struct{}),
	}
}

// DefineRole associates a list of permissions with a role, for a given tenant.
func (r *RBAC) DefineRole(tenantID tenant.TenantID, role tenant.Role, permissions ...Permission) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.definitions[tenantID] == nil {
		r.definitions[tenantID] = make(map[tenant.Role]map[Permission]struct{})
	}

	permSet := make(map[Permission]struct{}, len(permissions))
	for _, p := range permissions {
		permSet[p] = struct{}{}
	}
	r.definitions[tenantID][role] = permSet
}

// Actor is the minimal identity RBAC needs to answer "may this caller do
// this?" inside a tenant: which tenant, and which roles the caller holds
// there. It exists for tenants that are organizations with several members,
// where roles are per member rather than per tenant.
//
// Actor deliberately carries no notion of member, invitation or identity:
// tenant-core is not a user-management system. The application builds an
// Actor from its own user model. It is unrelated to admin.Principal, which
// is the authenticated caller of the Admin API.
type Actor struct {
	TenantID tenant.TenantID
	Roles    []tenant.Role
}

// Can checks whether the given tenant, via one of its roles, has the
// requested permission. It is equivalent to CanActor with an Actor built
// from the tenant's ID and roles.
func (r *RBAC) Can(t *tenant.Tenant, permission Permission) bool {
	return r.CanActor(Actor{TenantID: t.ID, Roles: t.Roles}, permission)
}

// CanActor checks whether the actor, via one of its roles within its
// tenant, has the requested permission. Roles are resolved against the
// actor's tenant only; an unknown tenant or role grants nothing.
func (r *RBAC) CanActor(actor Actor, permission Permission) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tenantRoles, ok := r.definitions[actor.TenantID]
	if !ok {
		return false
	}

	for _, role := range actor.Roles {
		if permSet, ok := tenantRoles[role]; ok {
			if _, has := permSet[permission]; has {
				return true
			}
		}
	}
	return false
}
