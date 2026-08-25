package middleware

import (
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"
)

// Wrap wraps next with tenant resolution. If the tenant cannot
// be identified or retrieved, the request is rejected with 404 before
// reaching next — see spec UC2.
func Wrap(m *tenant.Manager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, err := m.Resolve(r)
		if err != nil {
			http.Error(w, "tenant not found", http.StatusNotFound)
			return // stop here, next is NEVER called
		}

		ctx := tenantctx.WithTenant(r.Context(), t)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
