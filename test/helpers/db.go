// Package helpers holds shared test setup for integration tests.
package helpers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
	_ "time/tzdata" // zone names must load on hosts without a zone database

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"recurringjob/internal/common/auth"
	"recurringjob/internal/migrator"
)

// Connect opens the database named by TEST_DATABASE_URL and closes it when the
// test ends. The test is skipped when TEST_DATABASE_URL is unset.
func Connect(t *testing.T) *gorm.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	db, err := gorm.Open(postgres.Open(url), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// NewSchema creates an isolated tenant schema with the migrations applied and
// drops it when the test ends. The returned context carries the schema, as
// the auth middleware would set it.
func NewSchema(t *testing.T, db *gorm.DB) (string, context.Context) {
	t.Helper()
	schema := RandomSchemaName("t_")
	if _, err := migrator.Apply(db, schema, MigrationsDir()); err != nil {
		t.Fatalf("migrate %s: %v", schema, err)
	}
	t.Cleanup(func() { _ = migrator.DropSchema(db, schema) })
	return schema, auth.WithTenantSchema(context.Background(), schema)
}

// NewTestDB is Connect + NewSchema.
func NewTestDB(t *testing.T) (*gorm.DB, context.Context) {
	t.Helper()
	db := Connect(t)
	_, ctx := NewSchema(t, db)
	return db, ctx
}

// RandomSchemaName returns prefix + 12 random hex characters (a valid schema name).
func RandomSchemaName(prefix string) string {
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	return prefix + hex.EncodeToString(buf)
}

// MigrationsDir is the absolute path of the repository's migrations folder.
func MigrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

// ApplyLocalTZ makes the process's local time zone a fixed offset taken from
// TEST_LOCAL_TZ (whole hours, e.g. "7" or "-8"). Call it from TestMain to
// check that nothing depends on the machine's time zone.
func ApplyLocalTZ() {
	v := os.Getenv("TEST_LOCAL_TZ")
	if v == "" {
		return
	}
	hours, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("TEST_LOCAL_TZ must be whole hours, got %q", v))
	}
	time.Local = time.FixedZone("TEST"+v, hours*3600)
}
