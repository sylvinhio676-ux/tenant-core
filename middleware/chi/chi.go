package chi

import (
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"
)

// Middleware retourne un middleware Chi qui résout le tenant et l'injecte
// dans le contexte standard de la requête.
//
// Si le tenant ne peut pas être résolu ou récupéré, la requête est rejetée
// avec une réponse HTTP 404.
func Middleware(m *tenant.Manager) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t, err := m.Resolve(r)
			if err != nil {
				http.Error(
					w,
					"tenant not found",
					http.StatusNotFound,
				)
				return
			}

			ctx := tenantctx.WithTenant(r.Context(), t)
			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)
		})
	}
}