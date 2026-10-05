package repository_test

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
	"recurringjob/internal/repository"
	"recurringjob/test/fixtures"
	"recurringjob/test/helpers"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestJobRepositoryCreateAndGet(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	jobs := repository.NewJobRepository(db)

	t.Run("a one-off job round-trips, with a generated id and default status", func(t *testing.T) {
		in := &model.Job{Date: civil.New(2026, 10, 2)} // no status: the database default applies
		if err := jobs.Create(ctx, withRefs(t, ctx, db, in)); err != nil {
			t.Fatal(err)
		}
		if !uuidRe.MatchString(in.ID) || in.CreatedAt.IsZero() || in.UpdatedAt.IsZero() {
			t.Fatalf("not populated after create: %+v", in)
		}
		got, err := jobs.GetJob(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if civil.Format(got.Date) != "2026-10-02" || got.Date.Location() != time.UTC || got.Status != "unconfirmed" || got.Recurrence != nil {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("a nil recurrence is stored as SQL NULL, not the JSON text null", func(t *testing.T) {
		j := fixtures.OneOffJob(civil.New(2026, 10, 2))
		if err := jobs.Create(ctx, withRefs(t, ctx, db, j)); err != nil {
			t.Fatal(err)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM jobs WHERE id = ? AND recurrence IS NULL`, j.ID); n != 1 {
			t.Fatalf("recurrence was not NULL (matches=%d)", n)
		}
	})

	t.Run("every recurrence field survives the JSONB round trip", func(t *testing.T) {
		zero := 0 // Sunday: a zero pointer value must not be dropped
		rule := recurrence.Rule{
			Frequency: recurrence.FreqWeekly, Interval: 2, WeeklyPeriod: recurrence.WeeklySecondFour,
			WeeklyDaysOfWeek: []int{0, 3, 6}, MonthlyRepeatBy: recurrence.RepeatDayOfWeek, YearlyRepeatBy: recurrence.RepeatDayOfYear,
			EndsType: recurrence.EndsOnDate, EndsAfterCount: 7, EndsOnDate: "2027-01-31",
			ExceptType: recurrence.ExceptCondition, ExceptMonths: []int{1, 12},
			ExceptConditionEvery: recurrence.ConditionEveryMonth, ExceptConditionPeriod: "last", ExceptConditionDayOfWeek: &zero,
			ExceptJobID: "4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2",
		}
		in := &model.Job{Date: civil.New(2026, 10, 2), Status: "confirmed", Recurrence: &rule}
		if err := jobs.Create(ctx, withRefs(t, ctx, db, in)); err != nil {
			t.Fatal(err)
		}
		got, err := jobs.GetJob(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Recurrence, &rule) {
			t.Fatalf("recurrence changed\n got  %+v\n want %+v", got.Recurrence, &rule)
		}
		if got.Recurrence.ExceptConditionDayOfWeek == nil || *got.Recurrence.ExceptConditionDayOfWeek != 0 {
			t.Fatal("the Sunday (0) weekday was lost")
		}
		if got.Status != "confirmed" {
			t.Fatalf("status %q", got.Status)
		}
	})

	t.Run("a minimal rule keeps only its set fields", func(t *testing.T) {
		in := fixtures.DailyJob(civil.New(2026, 10, 2))
		if err := jobs.Create(ctx, withRefs(t, ctx, db, in)); err != nil {
			t.Fatal(err)
		}
		got, err := jobs.GetJob(ctx, in.ID)
		if err != nil || !reflect.DeepEqual(got.Recurrence, in.Recurrence) {
			t.Fatalf("got %+v err=%v", got.Recurrence, err)
		}
	})

	t.Run("dates at the calendar edges round-trip", func(t *testing.T) {
		for _, s := range []string{"0001-01-01", "1999-12-31", "2000-02-29", "9999-12-31"} {
			d, _ := civil.Parse(s)
			in := &model.Job{Date: d}
			if err := jobs.Create(ctx, withRefs(t, ctx, db, in)); err != nil {
				t.Fatalf("%s: %v", s, err)
			}
			got, err := jobs.GetJob(ctx, in.ID)
			if err != nil || civil.Format(got.Date) != s {
				t.Errorf("%s: got %v err=%v", s, got, err)
			}
		}
	})
}

func TestJobRepositoryErrors(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	jobs := repository.NewJobRepository(db)

	t.Run("an unknown id is a 404", func(t *testing.T) {
		_, err := jobs.GetJob(ctx, "00000000-0000-0000-0000-0000000000ff")
		var ae *apperror.AppError
		if !errors.As(err, &ae) || ae.Status != 404 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a malformed id is a database error, not a 404 (the service validates ids first)", func(t *testing.T) {
		_, err := jobs.GetJob(ctx, "not-a-uuid")
		var ae *apperror.AppError
		if err == nil || errors.As(err, &ae) {
			t.Fatalf("got %v", err)
		}
		if code := pgCode(err); code != "22P02" {
			t.Fatalf("pg code %q, want 22P02 (invalid_text_representation)", code)
		}
	})

	t.Run("the database refuses statuses a job may not have", func(t *testing.T) {
		for _, bad := range []string{"rescheduled", "bogus", "Confirmed"} {
			err := jobs.Create(ctx, withRefs(t, ctx, db, &model.Job{Date: civil.New(2026, 10, 2), Status: bad}))
			if code := pgCode(err); code != "23514" {
				t.Errorf("%q: pg code %q (err=%v), want 23514 check_violation", bad, code, err)
			}
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM jobs`); n != 0 {
			t.Fatalf("%d rows were stored", n)
		}
	})
}

// withRefs gives a job the schema's shared customer, location and service
// type and a default time and length, unless it already has them.
func withRefs(t *testing.T, ctx context.Context, db *gorm.DB, j *model.Job) *model.Job {
	t.Helper()
	if j.CustomerID == "" {
		r := helpers.RefsFor(t, ctx, db)
		j.CustomerID, j.LocationID, j.ServiceTypeID = r.CustomerID, r.LocationID, r.ServiceTypeID
	}
	if j.StartTime == "" {
		j.StartTime = "09:00:00"
	}
	if j.LengthMinutes == 0 {
		j.LengthMinutes = 60
	}
	return j
}

func TestJobRepositoryReferences(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	jobs := repository.NewJobRepository(db)
	r := helpers.RefsFor(t, ctx, db)
	other := helpers.SeedRefs(t, ctx, db)

	t.Run("a job loads with its customer, location and service type", func(t *testing.T) {
		in := withRefs(t, ctx, db, &model.Job{Date: civil.New(2026, 10, 2), StartTime: "13:45:00", LengthMinutes: 120})
		if err := jobs.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
		got, err := jobs.GetJob(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.StartTime != "13:45:00" || got.LengthMinutes != 120 || got.CustomerID != r.CustomerID {
			t.Fatalf("got %+v", got)
		}
		if got.Customer == nil || got.Customer.Name != "Ada Lovelace" || got.Customer.Email == nil || *got.Customer.Email != "ada@example.com" || got.Customer.Phone != nil {
			t.Fatalf("customer %+v", got.Customer)
		}
		if got.Location == nil || got.Location.AddressLine1 != "1 Main Street" || got.Location.CustomerID != r.CustomerID || got.Location.State != nil {
			t.Fatalf("location %+v", got.Location)
		}
		if got.ServiceType == nil || got.ServiceType.Name != "Window cleaning" || got.ServiceType.Description != nil {
			t.Fatalf("service type %+v", got.ServiceType)
		}
	})

	t.Run("saving a job never writes through its loaded customer, location or service type", func(t *testing.T) {
		in := withRefs(t, ctx, db, &model.Job{Date: civil.New(2026, 10, 3)})
		in.Customer = &model.Customer{ID: other.CustomerID, Name: "Changed In Memory"}
		in.Location = &model.Location{ID: other.LocationID, CustomerID: other.CustomerID, AddressLine1: "Changed"}
		in.ServiceType = &model.ServiceType{ID: other.ServiceTypeID, Name: "Changed"}
		if err := jobs.Create(ctx, in); err != nil {
			t.Fatal(err)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customers WHERE name = 'Changed In Memory'`); n != 0 {
			t.Fatal("the customer was overwritten through the job")
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM service_types WHERE name = 'Changed'`); n != 0 {
			t.Fatal("the service type was overwritten through the job")
		}
		got, _ := jobs.GetJob(ctx, in.ID)
		if got.CustomerID != r.CustomerID || got.Customer.Name != "Ada Lovelace" {
			t.Fatalf("the job's own ids decide: %+v", got)
		}
	})

	t.Run("the database enforces references and the length range", func(t *testing.T) {
		const none = "00000000-0000-0000-0000-0000000000ff"
		tests := []struct {
			name   string
			mutate func(j *model.Job)
			code   string
		}{
			{"unknown customer", func(j *model.Job) { j.CustomerID = none }, "23503"},
			{"unknown location", func(j *model.Job) { j.LocationID = none }, "23503"},
			{"unknown service type", func(j *model.Job) { j.ServiceTypeID = none }, "23503"},
			{"zero length", func(j *model.Job) { j.LengthMinutes = -1 }, "23514"},
			{"longer than a day", func(j *model.Job) { j.LengthMinutes = 1441 }, "23514"},
		}
		for _, tt := range tests {
			j := withRefs(t, ctx, db, &model.Job{Date: civil.New(2026, 10, 4)})
			tt.mutate(j)
			if code := pgCode(jobs.Create(ctx, j)); code != tt.code {
				t.Errorf("%s: pg code %q, want %s", tt.name, code, tt.code)
			}
		}
	})

	t.Run("a customer or location with jobs cannot be deleted", func(t *testing.T) {
		for _, q := range []string{`DELETE FROM customers WHERE id = ?`, `DELETE FROM locations WHERE id = ?`, `DELETE FROM service_types WHERE id = ?`} {
			id := map[string]string{"customers": r.CustomerID, "locations": r.LocationID, "service_types": r.ServiceTypeID}[q[len("DELETE FROM "):len(q)-len(" WHERE id = ?")]]
			err := helpers.Tx(ctx, db, func(tx *gorm.DB) error { return tx.Exec(q, id).Error })
			if code := pgCode(err); code != "23001" {
				t.Errorf("%s: pg code %q (err=%v), want 23001 restrict_violation", q, code, err)
			}
		}
	})
}
