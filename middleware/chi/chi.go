package chi

import (
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"
)

// Middleware returns a Chi middleware that resolves the tenant and injects
// it into the request's standard context.
//
// If the tenant cannot be resolved or retrieved, the request is rejected
// with an HTTP 404 response.
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
