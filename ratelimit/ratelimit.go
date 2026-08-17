package ratelimit

import (
	"sync"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"golang.org/x/time/rate"
)

// LimitFunc détermine la limite de requêtes par seconde applicable à un
// tenant donné. Injectée depuis l'application, pour que TenantRateLimiter
// reste totalement indépendant de la logique métier (plans, abonnements...).
type LimitFunc func(t *tenant.Tenant) rate.Limit

// TenantRateLimiter applique des quotas de requêtes différenciés par tenant.
type TenantRateLimiter struct {
	limitFunc LimitFunc
	burst     int
	limiters  sync.Map // TenantID -> *rate.Limiter
}

// NewTenantRateLimiter crée un TenantRateLimiter. burst est la taille de
// rafale autorisée, identique pour tous les tenants (simplification pour
// l'instant — pourrait devenir différencié par tenant plus tard si besoin).
func NewTenantRateLimiter(limitFunc LimitFunc, burst int) *TenantRateLimiter {
	return &TenantRateLimiter{
		limitFunc: limitFunc,
		burst:     burst,
	}
}

// Allow indique si une requête pour ce tenant est autorisée maintenant,
// selon son quota. Crée le *rate.Limiter du tenant à la première rencontre.
func (rl *TenantRateLimiter) Allow(t *tenant.Tenant) bool {
	limiter := rl.getLimiter(t)
	return limiter.Allow()
}

func (rl *TenantRateLimiter) getLimiter(t *tenant.Tenant) *rate.Limiter {
	if v, ok := rl.limiters.Load(t.ID); ok {
		return v.(*rate.Limiter)
	}

	limit := rl.limitFunc(t)
	newLimiter := rate.NewLimiter(limit, rl.burst)

	actual, _ := rl.limiters.LoadOrStore(t.ID, newLimiter)
	return actual.(*rate.Limiter)
}