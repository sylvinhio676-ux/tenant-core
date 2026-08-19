package tenant

import (
	"context"
	"net/http"
)

type TenantID string

// State représente l'état d'un tenant.
type State string

const (
    // Active signifie que le tenant peut traiter des requêtes normalement.
    Active State = "active"
    // Disabled signifie que le tenant est désactivé (ex: fin d'abonnement).
    // La désactivation tolère un délai de propagation (cache TTL).
    Disabled State = "disabled"
    // Banned signifie que le tenant est banni pour fraude/abus.
    // Le bannissement doit être appliqué immédiatement, sans délai de cache.
    Banned State = "banned"
)

// Tenant représente un client isolé du système multi-tenant.
type Tenant struct {
	ID    TenantID
	State State
	Roles []string
}

// Resolver identifie le tenant à partir d'une requête HTTP entrante.
type Resolver interface {
	Resolve(r *http.Request) (TenantID, error)
}

// Store est la source de vérité pour l'état des tenants.
type Store interface {
	Get(ctx context.Context, id TenantID) (*Tenant, error)
	IsBanned(ctx context.Context, id TenantID) (bool, error)
}

// Manager assemble les composants du toolkit et orchestre le chemin de
// résolution d'une requête HTTP vers un contexte tenant.
type Manager struct {
	resolver Resolver
	store    Store
}

// Option configure un Manager au moment de sa création.
type Option func(*Manager)

// WithResolver définit le Resolver utilisé pour identifier le tenant
// depuis une requête HTTP. Obligatoire.
func WithResolver(r Resolver) Option {
	return func(m *Manager) {
		m.resolver = r
	}
}

// WithStore définit le Store utilisé pour récupérer les informations
// complètes d'un tenant. Obligatoire.
func WithStore(s Store) Option {
	return func(m *Manager) {
		m.store = s
	}
}

/**
 * New crée un Manager à partir des options fournies. Panique si Resolver
	ou Store ne sont pas configurés — une erreur de configuration du
	programme doit être détectée immédiatement, pas gérée comme une erreur
	de traitement de requête.
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

/**
 * Resolve identifie le tenant à partir d'une requête HTTP, puis récupère
    ses informations complètes. Ne construit PAS de context.Context —
    cette responsabilité appartient au package tenantctx, utilisé par les
    middlewares (voir cahier des charges section 7, chemin de requête).
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