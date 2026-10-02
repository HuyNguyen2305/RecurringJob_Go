package repository_test

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/test/fixtures"
	"recurringjob/test/helpers"
)

func TestOccurrenceRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	occs := repository.NewOccurrenceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	other := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))

	row := func(jobID, date, status string) *model.JobOccurrence {
		d, _ := civil.Parse(date)
		return &model.JobOccurrence{JobID: jobID, OccurrenceDate: d, Status: status}
	}

	t.Run("Create fills id and timestamps; ListByJob returns rows by date, scoped to the job", func(t *testing.T) {
		for _, d := range []string{"2026-10-09", "2026-10-02", "2026-10-16"} {
			r := row(job.ID, d, "confirmed")
			if err := occs.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			if !uuidRe.MatchString(r.ID) || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
				t.Fatalf("not populated: %+v", r)
			}
		}
		if err := occs.Create(ctx, row(other.ID, "2026-10-01", "completed")); err != nil {
			t.Fatal(err)
		}
		got, err := occs.ListByJob(ctx, job.ID)
		if err != nil || len(got) != 3 {
			t.Fatalf("len=%d err=%v", len(got), err)
		}
		for i, want := range []string{"2026-10-02", "2026-10-09", "2026-10-16"} {
			if civil.Format(got[i].OccurrenceDate) != want || got[i].OccurrenceDate.Location() != time.UTC {
				t.Errorf("row %d: %v", i, got[i].OccurrenceDate)
			}
		}
		if none, err := occs.ListByJob(ctx, "00000000-0000-0000-0000-0000000000ff"); err != nil || len(none) != 0 {
			t.Fatalf("unknown job: %v %v", none, err)
		}
	})

	t.Run("a duplicate (job, date) is a 409 and stores nothing", func(t *testing.T) {
		before := helpers.CountOccurrences(t, ctx, db, job.ID)
		err := occs.Create(ctx, row(job.ID, "2026-10-09", "completed"))
		var ae *apperror.AppError
		if !errors.As(err, &ae) || ae.Status != 409 {
			t.Fatalf("got %v", err)
		}
		if after := helpers.CountOccurrences(t, ctx, db, job.ID); after != before {
			t.Fatalf("rows %d -> %d", before, after)
		}
	})

	t.Run("the same date on a different job is allowed", func(t *testing.T) {
		if err := occs.Create(ctx, row(other.ID, "2026-10-09", "confirmed")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an unknown job is a foreign-key error, not a 409", func(t *testing.T) {
		err := occs.Create(ctx, row("00000000-0000-0000-0000-0000000000ff", "2026-10-02", "confirmed"))
		var ae *apperror.AppError
		if err == nil || errors.As(err, &ae) || pgCode(err) != "23503" {
			t.Fatalf("got %v (pg %q)", err, pgCode(err))
		}
	})

	t.Run("the database refuses an unknown status, accepts all seven valid ones", func(t *testing.T) {
		if code := pgCode(occs.Create(ctx, row(job.ID, "2026-11-01", "bogus"))); code != "23514" {
			t.Fatalf("pg code %q, want 23514", code)
		}
		j := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		for i, st := range []string{"unconfirmed", "confirmed", "in_progress", "completed", "canceled", "terminate_service", "rescheduled"} {
			if err := occs.Create(ctx, row(j.ID, civil.Format(civil.New(2026, 12, 1+i)), st)); err != nil {
				t.Errorf("%s: %v", st, err)
			}
		}
	})

	t.Run("reschedule dates and completed_at round-trip", func(t *testing.T) {
		j := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
		to, from := civil.New(2026, 10, 12), civil.New(2026, 10, 9)
		done := time.Date(2026, 10, 9, 14, 30, 5, 0, time.FixedZone("+7", 7*3600))
		a := row(j.ID, "2026-10-09", "rescheduled")
		a.RescheduledTo = &to
		b := row(j.ID, "2026-10-12", "completed")
		b.RescheduledFrom = &from
		b.CompletedAt = &done
		if err := occs.Create(ctx, a); err != nil {
			t.Fatal(err)
		}
		if err := occs.Create(ctx, b); err != nil {
			t.Fatal(err)
		}
		got, _ := occs.ListByJob(ctx, j.ID)
		if len(got) != 2 {
			t.Fatalf("len %d", len(got))
		}
		if got[0].RescheduledTo == nil || civil.Format(*got[0].RescheduledTo) != "2026-10-12" || got[0].RescheduledFrom != nil || got[0].CompletedAt != nil {
			t.Errorf("first row: %+v", got[0])
		}
		if got[1].RescheduledFrom == nil || civil.Format(*got[1].RescheduledFrom) != "2026-10-09" || got[1].RescheduledTo != nil {
			t.Errorf("second row: %+v", got[1])
		}
		if got[1].CompletedAt == nil || !got[1].CompletedAt.Equal(done) {
			t.Errorf("completed_at %v, want the instant %v", got[1].CompletedAt, done)
		}
	})
}

func TestOccurrenceRepositoryUpdateGuarded(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	occs := repository.NewOccurrenceRepository(db)
	job := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	d := civil.New(2026, 10, 2)
	seed := func(status string) *model.JobOccurrence {
		d = civil.AddDays(d, 1)
		r := &model.JobOccurrence{JobID: job.ID, OccurrenceDate: d, Status: status}
		if err := occs.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	stored := func(r *model.JobOccurrence) model.JobOccurrence {
		rows, _ := occs.ListByJob(ctx, r.JobID)
		for _, x := range rows {
			if x.ID == r.ID {
				return x
			}
		}
		t.Fatal("row vanished")
		return model.JobOccurrence{}
	}

	t.Run("updates when the status is allowed, and persists every field", func(t *testing.T) {
		r := seed("confirmed")
		prev := stored(r).UpdatedAt
		time.Sleep(15 * time.Millisecond)
		now := time.Now().UTC()
		n, err := occs.UpdateGuarded(ctx, r.ID, []string{"unconfirmed", "confirmed"}, map[string]any{"status": "completed", "completed_at": now})
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		got := stored(r)
		if got.Status != "completed" || got.CompletedAt == nil || !got.CompletedAt.Equal(now.Truncate(time.Microsecond)) {
			t.Fatalf("got %+v", got)
		}
		if !got.UpdatedAt.After(prev) {
			t.Fatalf("updated_at not advanced: %v -> %v", prev, got.UpdatedAt)
		}
	})

	t.Run("does nothing when the status is not in the allowed list", func(t *testing.T) {
		r := seed("completed")
		n, err := occs.UpdateGuarded(ctx, r.ID, []string{"unconfirmed", "confirmed", "in_progress"}, map[string]any{"status": "canceled"})
		if err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if got := stored(r); got.Status != "completed" {
			t.Fatalf("status changed to %q", got.Status)
		}
	})

	t.Run("an empty allowed list matches nothing", func(t *testing.T) {
		r := seed("unconfirmed")
		if n, err := occs.UpdateGuarded(ctx, r.ID, nil, map[string]any{"status": "confirmed"}); err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("an unknown id affects no rows", func(t *testing.T) {
		n, err := occs.UpdateGuarded(ctx, "00000000-0000-0000-0000-0000000000ff", []string{"unconfirmed"}, map[string]any{"status": "confirmed"})
		if err != nil || n != 0 {
			t.Fatalf("n=%d err=%v", n, err)
		}
	})

	t.Run("it only touches the addressed row", func(t *testing.T) {
		a, b := seed("unconfirmed"), seed("unconfirmed")
		if n, _ := occs.UpdateGuarded(ctx, a.ID, []string{"unconfirmed"}, map[string]any{"status": "confirmed"}); n != 1 {
			t.Fatal("update failed")
		}
		if stored(b).Status != "unconfirmed" {
			t.Fatal("a neighbouring row changed")
		}
	})

	t.Run("a status the database refuses is an error, and the row is unchanged", func(t *testing.T) {
		r := seed("unconfirmed")
		_, err := occs.UpdateGuarded(ctx, r.ID, []string{"unconfirmed"}, map[string]any{"status": "bogus"})
		if pgCode(err) != "23514" {
			t.Fatalf("got %v (pg %q)", err, pgCode(err))
		}
		if stored(r).Status != "unconfirmed" {
			t.Fatal("row changed")
		}
	})

	t.Run("two sequential guarded updates: the second sees the first and affects nothing", func(t *testing.T) {
		r := seed("unconfirmed")
		allowed := []string{"unconfirmed", "confirmed", "in_progress"}
		n1, _ := occs.UpdateGuarded(ctx, r.ID, allowed, map[string]any{"status": "completed"})
		n2, _ := occs.UpdateGuarded(ctx, r.ID, allowed, map[string]any{"status": "canceled"})
		if n1 != 1 || n2 != 0 || stored(r).Status != "completed" {
			t.Fatalf("n1=%d n2=%d status=%s", n1, n2, stored(r).Status)
		}
	})
}

func TestOccurrenceCascadeDelete(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	occs := repository.NewOccurrenceRepository(db)
	a := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	b := helpers.SeedJob(t, ctx, db, fixtures.DailyJob(civil.New(2026, 10, 1)))
	for _, j := range []*model.Job{a, b} {
		for i := 0; i < 3; i++ {
			if err := occs.Create(ctx, &model.JobOccurrence{JobID: j.ID, OccurrenceDate: civil.New(2026, 10, 2+i), Status: "confirmed"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	helpers.MustTx(t, ctx, db, func(tx *gorm.DB) error { return tx.Exec(`DELETE FROM jobs WHERE id = ?`, a.ID).Error })
	if n := helpers.CountOccurrences(t, ctx, db, a.ID); n != 0 {
		t.Fatalf("%d rows left for the deleted job", n)
	}
	if n := helpers.CountOccurrences(t, ctx, db, b.ID); n != 3 {
		t.Fatalf("the other job lost rows: %d", n)
	}
}
