package auth_test

import (
	"context"
	"testing"

	"recurringjob/internal/common/auth"
)

func TestTenantSchemaContext(t *testing.T) {
	if got := auth.TenantSchemaFromContext(context.Background()); got != "" {
		t.Fatalf("empty context returned %q", got)
	}
	ctx := auth.WithTenantSchema(context.Background(), "tenant_a")
	if got := auth.TenantSchemaFromContext(ctx); got != "tenant_a" {
		t.Fatalf("got %q", got)
	}
	// A later value shadows the earlier one without mutating the parent.
	child := auth.WithTenantSchema(ctx, "tenant_b")
	if auth.TenantSchemaFromContext(child) != "tenant_b" || auth.TenantSchemaFromContext(ctx) != "tenant_a" {
		t.Fatal("derived context leaked into the parent")
	}
	// An unrelated key with the same string value must not collide.
	type other struct{}
	if got := auth.TenantSchemaFromContext(context.WithValue(context.Background(), other{}, "x")); got != "" {
		t.Fatalf("collision: %q", got)
	}
}
