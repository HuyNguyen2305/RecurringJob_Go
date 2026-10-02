package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/auth"
)

func TestTenantSchemaMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"header wins", "tenant_x", "tenant_x"},
		{"falls back to the default", "", "fallback_schema"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			r := gin.New()
			r.Use(auth.TenantSchema("fallback_schema"))
			r.GET("/x", func(c *gin.Context) {
				seen = auth.TenantSchemaFromContext(c.Request.Context())
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tt.header != "" {
				req.Header.Set(auth.TenantHeader, tt.header)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusNoContent || seen != tt.want {
				t.Fatalf("status %d, schema %q, want %q", w.Code, seen, tt.want)
			}
		})
	}
}
