package echo

import (
	"net/http"

	"github.com/labstack/echo/v4"
	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"
)

// Middleware returns an Echo middleware that resolves the tenant and injects
// it into the request's standard context, accessible afterwards via
// tenantctx.FromContext(...) in subsequent Echo handlers.
func Middleware(m *tenant.Manager) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			t, err := m.Resolve(c.Request())
			if err != nil {
				return echo.NewHTTPError(http.StatusNotFound, "tenant not found")
			}

			ctx := tenantctx.WithTenant(c.Request().Context(), t)
			c.SetRequest(c.Request().WithContext(ctx))

			return next(c)
		}
	}
}
