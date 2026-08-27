package tenant

import (
	"context"
	"errors"
	"net/http"
)

type TenantID string

// Role names a capability grouping that can be attached to a Tenant and
// checked by the rbac package. This type lives here, in the root package,
// rather than in rbac — even though rbac is its primary consumer — for the
// same reason TenantID does: Tenant.Roles needs it, and the dependency
// direction established throughout this toolkit is tenant → rbac, never the
// reverse. Defining Role in rbac would force this root package to import
// rbac just to type its own Tenant.Roles field, creating an import cycle.
// A string literal like "admin" can still be passed directly wherever a
// Role is expected, since Go implicitly converts an untyped string
// constant to any named string type.
type Role string

// State represents the state of a tenant.
type State string

const (
	// Active means the tenant can process requests normally.
	Active State = "active"
	// Disabled means the tenant is disabled (e.g. subscription ended).
	// Disabling tolerates a propagation delay (cache TTL).
	Disabled State = "disabled"
	// Banned means the tenant is banned for fraud/abuse.
	// Banning must be applied immediately, with no cache delay.
	Banned State = "banned"
)

// Tenant represents an isolated client of the multi-tenant system.
type Tenant struct {
	ID    TenantID
	State State
	Roles []Role
}

// Resolver identifies the tenant from an incoming HTTP request.
type Resolver interface {
	Resolve(r *http.Request) (TenantID, error)
}

// Store is the source of truth for tenant state.
type Store interface {
	Get(ctx context.Context, id TenantID) (*Tenant, error)
	IsBanned(ctx context.Context, id TenantID) (bool, error)
}

/*
*
  - AdminStore exposes the write capabilities needed for tenant
    administration (creation, modification, state changes). Separated from
    Store, which remains strictly read-only for the normal resolution
    path — Manager never depends on AdminStore.
*/
type AdminStore interface {
	Create(ctx context.Context, t *Tenant) error
	Update(ctx context.Context, t *Tenant) error
	SetState(ctx context.Context, id TenantID, state State) error
}

// ErrTenantNotFound is the sentinel error a Store/AdminStore
// implementation must return (directly, or wrapped with %w) when the
// requested tenant does not exist. This is part of the Store/AdminStore
// contract, not just a MemoryStore detail: callers such as
// admin.HTTPHandler rely on errors.Is(err, tenant.ErrTenantNotFound) to
// map it to the correct HTTP status, regardless of which Store
// implementation is actually in use.
var ErrTenantNotFound = errors.New("tenant not found")

// ErrTenantAlreadyExists is the sentinel error an AdminStore
// implementation must return (directly, or wrapped with %w) from Create
// when the given tenant ID is already in use. Same contract as
// ErrTenantNotFound: every implementation must reuse this error, never
// define its own equivalent.
var ErrTenantAlreadyExists = errors.New("tenant already exists")

// Manager assembles the toolkit's components and orchestrates the path
// from resolving an HTTP request to a tenant context.
type Manager struct {
	resolver Resolver
	store    Store
}

// Option configures a Manager at creation time.
type Option func(*Manager)

// WithResolver sets the Resolver used to identify the tenant
// from an HTTP request. Required.
func WithResolver(r Resolver) Option {
	return func(m *Manager) {
		m.resolver = r
	}
}

// WithStore sets the Store used to retrieve a tenant's full
// information. Required.
func WithStore(s Store) Option {
	return func(m *Manager) {
		m.store = s
	}
}

/*
*
  - New creates a Manager from the given options. Panics if Resolver
    or Store are not configured — a program configuration error must be
    caught immediately, not handled as a request processing error.
*/
func New(options ...Option) *Manager {
	m := &Manager{}
	for _, opt := range options {
		opt(m)
	}

	if m.resolver == nil {
		panic("tenant: resolver is required")
	}
	if m.store == nil {
		panic("tenant: store is required")
	}

	return m
}

/*
*
  - Resolve identifies the tenant from an HTTP request, then retrieves
    its full information. Does NOT build a context.Context —
    that responsibility belongs to the tenantctx package, used by
    middlewares (see spec section 7, request path).
*/
func (m *Manager) Resolve(r *http.Request) (*Tenant, error) {
	id, err := m.resolver.Resolve(r)
	if err != nil {
		return nil, err
	}

	t, err := m.store.Get(r.Context(), id)
	if err != nil {
		return nil, err
	}

	return t, nil
}
