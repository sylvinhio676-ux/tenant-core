package gin

import (
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	"github.com/gin-gonic/gin"
)

// Middleware returns a Gin middleware that resolves the tenant and injects
// it into the request's standard context (accessible afterwards via
// tenantctx.FromContext), regardless of the framework used.
func Middleware(m *tenant.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, err := m.Resolve(c.Request)
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		ctx := tenantctx.WithTenant(c.Request.Context(), t)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}
