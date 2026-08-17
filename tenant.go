package tenant

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