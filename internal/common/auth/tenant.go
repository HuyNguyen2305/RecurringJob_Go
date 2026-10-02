// Package auth carries the request identity in context.Context.
package auth

import "context"

type tenantKey struct{}

// WithTenantSchema stores the tenant's Postgres schema name in ctx. The
// auth middleware calls this; repositories read it to set the search_path.
func WithTenantSchema(ctx context.Context, schema string) context.Context {
	return context.WithValue(ctx, tenantKey{}, schema)
}

// TenantSchemaFromContext returns the tenant schema, or "" when none is set.
func TenantSchemaFromContext(ctx context.Context) string {
	s, _ := ctx.Value(tenantKey{}).(string)
	return s
}
