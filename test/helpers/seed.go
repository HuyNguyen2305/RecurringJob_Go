package helpers

import (
	"context"
	"encoding/json"
	"testing"

	"gorm.io/gorm"

	"recurringjob/internal/common/auth"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

// Tx runs fn in a transaction whose search_path is the tenant schema in ctx.
func Tx(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT set_config('search_path', ?, true)`, auth.TenantSchemaFromContext(ctx)+", public").Error; err != nil {
			return err
		}
		return fn(tx)
	})
}

// MustTx is Tx that fails the test on error.
func MustTx(t *testing.T, ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) {
	t.Helper()
	if err := Tx(ctx, db, fn); err != nil {
		t.Fatalf("tenant transaction: %v", err)
	}
}

// SeedJob inserts job into the tenant schema carried by ctx.
func SeedJob(t *testing.T, ctx context.Context, db *gorm.DB, job *model.Job) *model.Job {
	t.Helper()
	MustTx(t, ctx, db, func(tx *gorm.DB) error { return tx.Create(job).Error })
	return job
}

// SetRecurrence overwrites a job's recurrence directly, bypassing validation
// (used to build states the API refuses, e.g. a cycle between two jobs).
func SetRecurrence(t *testing.T, ctx context.Context, db *gorm.DB, jobID string, rule recurrence.Rule) {
	t.Helper()
	raw, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	MustTx(t, ctx, db, func(tx *gorm.DB) error {
		return tx.Exec(`UPDATE jobs SET recurrence = ?::jsonb WHERE id = ?`, string(raw), jobID).Error
	})
}

// Scalar runs a single-value query in the tenant schema.
func Scalar(t *testing.T, ctx context.Context, db *gorm.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	MustTx(t, ctx, db, func(tx *gorm.DB) error { return tx.Raw(query, args...).Scan(&n).Error })
	return n
}

// CountOccurrences returns the number of stored occurrence rows for a job.
func CountOccurrences(t *testing.T, ctx context.Context, db *gorm.DB, jobID string) int64 {
	t.Helper()
	return Scalar(t, ctx, db, `SELECT count(*) FROM job_occurrences WHERE job_id = ?`, jobID)
}
