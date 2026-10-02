package auth

import "github.com/gin-gonic/gin"

// TenantHeader names the request header that selects the tenant schema.
const TenantHeader = "X-Tenant-Schema"

// TenantSchema is a development stand-in for the real auth strategies: it
// takes the tenant schema from TenantHeader (falling back to defaultSchema)
// and stores it in the request context for the repositories. It performs no
// authentication, so it must be replaced before this is exposed.
func TenantSchema(defaultSchema string) gin.HandlerFunc {
	return func(c *gin.Context) {
		schema := c.GetHeader(TenantHeader)
		if schema == "" {
			schema = defaultSchema
		}
		c.Request = c.Request.WithContext(WithTenantSchema(c.Request.Context(), schema))
		c.Next()
	}
}
