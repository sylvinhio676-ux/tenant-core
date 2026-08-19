package gin

import (
	"net/http"

	tenant "github.com/sylvinhio676-ux/tenant-core"
	"github.com/sylvinhio676-ux/tenant-core/tenantctx"

	"github.com/gin-gonic/gin"
)

// Middleware retourne un middleware Gin qui résout le tenant et l'injecte
// dans le contexte standard de la requête (accessible ensuite via
// tenantctx.FromContext), quel que soit le framework utilisé.
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