package helpers

import (
	"context"
	"encoding/json"
	"sync"
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

// Refs are the ids of a customer, one of its locations and a service type.
type Refs struct {
	CustomerID    string
	LocationID    string
	ServiceTypeID string
}

var (
	refsMu    sync.Mutex
	refsCache = map[string]Refs{}
)

// SeedRefs inserts a customer ("Ada Lovelace"), a location of that customer
// and a service type into the tenant schema carried by ctx, and returns their
// ids. Every call creates a new set.
func SeedRefs(t *testing.T, ctx context.Context, db *gorm.DB) Refs {
	t.Helper()
	email, city := "ada@example.com", "Springfield"
	customer := &model.Customer{Name: "Ada Lovelace", Email: &email}
	serviceType := &model.ServiceType{Name: "Window cleaning"}
	MustTx(t, ctx, db, func(tx *gorm.DB) error {
		if err := tx.Create(customer).Error; err != nil {
			return err
		}
		if err := tx.Create(serviceType).Error; err != nil {
			return err
		}
		return nil
	})
	location := &model.Location{CustomerID: customer.ID, AddressLine1: "1 Main Street", City: &city}
	MustTx(t, ctx, db, func(tx *gorm.DB) error { return tx.Create(location).Error })
	return Refs{CustomerID: customer.ID, LocationID: location.ID, ServiceTypeID: serviceType.ID}
}

// RefsFor returns one shared set of references per tenant schema, creating it
// on first use, for tests that do not care which customer a job belongs to.
func RefsFor(t *testing.T, ctx context.Context, db *gorm.DB) Refs {
	t.Helper()
	schema := auth.TenantSchemaFromContext(ctx)
	refsMu.Lock()
	defer refsMu.Unlock()
	if r, ok := refsCache[schema]; ok {
		return r
	}
	r := SeedRefs(t, ctx, db)
	refsCache[schema] = r
	return r
}

// SeedJob inserts job into the tenant schema carried by ctx. A job without a
// customer, location and service type gets the schema's shared set, and a
// missing start time and length default to 09:00 for 60 minutes.
func SeedJob(t *testing.T, ctx context.Context, db *gorm.DB, job *model.Job) *model.Job {
	t.Helper()
	if job.CustomerID == "" {
		r := RefsFor(t, ctx, db)
		job.CustomerID, job.LocationID, job.ServiceTypeID = r.CustomerID, r.LocationID, r.ServiceTypeID
	}
	if job.StartTime == "" {
		job.StartTime = "09:00:00"
	}
	if job.LengthMinutes == 0 {
		job.LengthMinutes = 60
	}
	MustTx(t, ctx, db, func(tx *gorm.DB) error {
		return tx.Omit("Customer", "Location", "ServiceType").Create(job).Error
	})
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
