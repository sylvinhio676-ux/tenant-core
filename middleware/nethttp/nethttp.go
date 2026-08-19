package middleware

import (
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"
)

// Wrap enveloppe next avec la résolution du tenant. Si le tenant ne peut
// pas être identifié ou récupéré, la requête est rejetée avec 404 avant
// d'atteindre next — voir cahier des charges UC2.
func Wrap(m *tenant.Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, err := m.Resolve(r)
		if err != nil {
			http.Error(w, "tenant not found", http.StatusNotFound)
			return // on s'arrête ici, next n'est JAMAIS appelé
		}

		ctx := tenantctx.WithTenant(r.Context(), t)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}