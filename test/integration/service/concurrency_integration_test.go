package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/internal/service"
	"recurringjob/test/helpers"
)

const workers = 8

// runConcurrently starts n goroutines at the same moment and returns their errors by index.
func runConcurrently(n int, fn func(i int) error) []error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = fn(i)
		}(i)
	}
	close(start)
	wg.Wait()
	return errs
}

// winners returns the indexes whose call succeeded, failing the test if any other call was not a clean 409.
func winners(t *testing.T, errs []error) []int {
	t.Helper()
	var ok []int
	for i, err := range errs {
		switch {
		case err == nil:
			ok = append(ok, i)
		case appStatus(err) != 409:
			t.Fatalf("worker %d: want success or 409, got %v", i, err)
		}
	}
	return ok
}

func TestIntegrationConcurrentStatusRace(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	svc := build(db, nil)
	today := civil.Today()
	statuses := []string{
		service.StatusConfirmed, service.StatusInProgress, service.StatusCompleted, service.StatusCanceled,
		service.StatusTerminateService, service.StatusConfirmed, service.StatusCanceled, service.StatusCompleted,
	}

	t.Run("different targets: exactly one final status wins and is what is stored", func(t *testing.T) {
		for round := 0; round < 10; round++ {
			job := helpers.SeedJob(t, ctx, db, &model.Job{Date: today, Status: "unconfirmed"})
			errs := runConcurrently(workers, func(i int) error {
				_, err := svc.UpdateOccurrence(ctx, job.ID, today, service.UpdateOccurrenceRequest{Status: statuses[i]})
				return err
			})
			ok := winners(t, errs)
			finals := 0
			winner := ""
			for _, i := range ok {
				if service.IsFinal(statuses[i]) {
					finals++
					winner = statuses[i]
				}
			}
			if finals != 1 {
				t.Fatalf("round %d: %d final transitions succeeded (%v), want exactly 1", round, finals, ok)
			}
			rows, _ := repoRows(t, ctx, db, job.ID)
			if len(rows) != 1 || rows[0].Status != winner {
				t.Fatalf("round %d: stored %+v, want one row with status %s", round, rows, winner)
			}
		}
	})

	t.Run("the same status from everyone: one winner, the rest conflict", func(t *testing.T) {
		for round := 0; round < 10; round++ {
			job := helpers.SeedJob(t, ctx, db, &model.Job{Date: today, Status: "unconfirmed"})
			errs := runConcurrently(workers, func(int) error {
				_, err := svc.UpdateOccurrence(ctx, job.ID, today, service.UpdateOccurrenceRequest{Status: service.StatusCompleted})
				return err
			})
			if ok := winners(t, errs); len(ok) != 1 {
				t.Fatalf("round %d: %d winners", round, len(ok))
			}
			if n := helpers.CountOccurrences(t, ctx, db, job.ID); n != 1 {
				t.Fatalf("round %d: %d rows", round, n)
			}
		}
	})

	t.Run("updates on different jobs never block each other", func(t *testing.T) {
		jobs := make([]*model.Job, workers)
		for i := range jobs {
			jobs[i] = helpers.SeedJob(t, ctx, db, &model.Job{Date: today, Status: "unconfirmed"})
		}
		errs := runConcurrently(workers, func(i int) error {
			_, err := svc.UpdateOccurrence(ctx, jobs[i].ID, today, service.UpdateOccurrenceRequest{Status: service.StatusCompleted})
			return err
		})
		for i, err := range errs {
			if err != nil {
				t.Errorf("job %d: %v", i, err)
			}
		}
	})
}

func repoRows(t *testing.T, ctx context.Context, db *gorm.DB, jobID string) ([]model.JobOccurrence, error) {
	t.Helper()
	rows, err := repository.NewOccurrenceRepository(db).ListByJob(ctx, jobID)
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	return rows, err
}

func TestIntegrationConcurrentReschedule(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	svc := build(db, nil)
	today := civil.Today()
	day := func(n int) time.Time { return civil.AddDays(today, n) }
	f := civil.Format

	newJob := func(t *testing.T) *model.Job {
		t.Helper()
		job := helpers.SeedJob(t, ctx, db, &model.Job{Date: today, Status: "unconfirmed", Recurrence: weeklyOn(today)})
		if _, err := svc.UpdateOccurrence(ctx, job.ID, day(0), service.UpdateOccurrenceRequest{Status: service.StatusCompleted}); err != nil {
			t.Fatal(err)
		}
		return job
	}
	move := func(jobID string, target int) error {
		to := day(target)
		_, err := svc.UpdateOccurrence(ctx, jobID, day(7), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: &to})
		return err
	}

	t.Run("different targets for one occurrence: one wins, the loser leaves no orphan visit", func(t *testing.T) {
		targets := []int{8, 9, 10, 11, 12, 13, 15, 16} // none is a series date (0, 7, 14, ...)
		for round := 0; round < 6; round++ {
			job := newJob(t)
			errs := runConcurrently(workers, func(i int) error { return move(job.ID, targets[i]) })
			ok := winners(t, errs)
			if len(ok) != 1 {
				t.Fatalf("round %d: %d winners", round, len(ok))
			}
			rows, _ := repoRows(t, ctx, db, job.ID)
			if len(rows) != 3 {
				t.Fatalf("round %d: %d rows, want 3 (completed, rescheduled original, one visit): %+v", round, len(rows), rows)
			}
			orig, visit := rows[1], rows[2]
			if f(orig.OccurrenceDate) != f(day(7)) || orig.Status != service.StatusRescheduled || orig.RescheduledTo == nil {
				t.Fatalf("round %d: original %+v", round, orig)
			}
			wantTarget := f(day(targets[ok[0]]))
			if f(visit.OccurrenceDate) != wantTarget || f(*orig.RescheduledTo) != wantTarget || visit.RescheduledFrom == nil || f(*visit.RescheduledFrom) != f(day(7)) {
				t.Fatalf("round %d: the stored pair does not match the winner (target %s): orig=%+v visit=%+v", round, wantTarget, orig, visit)
			}
		}
	})

	t.Run("the same target from everyone: one wins", func(t *testing.T) {
		for round := 0; round < 6; round++ {
			job := newJob(t)
			errs := runConcurrently(workers, func(int) error { return move(job.ID, 9) })
			if ok := winners(t, errs); len(ok) != 1 {
				t.Fatalf("round %d: %d winners", round, len(ok))
			}
			if n := helpers.CountOccurrences(t, ctx, db, job.ID); n != 3 {
				t.Fatalf("round %d: %d rows", round, n)
			}
		}
	})

	t.Run("a reschedule racing a plain status change on the same occurrence", func(t *testing.T) {
		for round := 0; round < 6; round++ {
			job := newJob(t)
			errs := runConcurrently(2, func(i int) error {
				if i == 0 {
					return move(job.ID, 9)
				}
				_, err := svc.UpdateOccurrence(ctx, job.ID, day(7), service.UpdateOccurrenceRequest{Status: service.StatusCanceled})
				return err
			})
			if ok := winners(t, errs); len(ok) != 1 {
				t.Fatalf("round %d: %d winners (%v)", round, len(ok), errs)
			}
			rows, _ := repoRows(t, ctx, db, job.ID)
			cancelWon := errs[1] == nil
			switch {
			case cancelWon && len(rows) != 2:
				t.Fatalf("round %d: cancel won but %d rows (an orphan visit?): %+v", round, len(rows), rows)
			case !cancelWon && len(rows) != 3:
				t.Fatalf("round %d: reschedule won but %d rows: %+v", round, len(rows), rows)
			}
		}
	})
}

func TestIntegrationReadersDuringWrites(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	svc := build(db, nil)
	today := civil.Today()
	job := helpers.SeedJob(t, ctx, db, &model.Job{Date: civil.AddDays(today, -5), Status: "unconfirmed", Recurrence: dailyRule()})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				items, err := svc.Schedule(ctx, job.ID, service.ScheduleQuery{Limit: 20})
				if err != nil {
					errCh <- err
					return
				}
				open := 0
				for _, it := range items {
					if service.IsOpen(it.Status) && (it.State == service.StateReal || it.State == service.StateOverdue) {
						open++
					}
				}
				if open > 1 {
					errCh <- &snapshotError{items}
					return
				}
			}
		}()
	}
	for d := -5; d <= -1; d++ { // complete the five past days in order while readers hammer the schedule
		if _, err := svc.UpdateOccurrence(ctx, job.ID, civil.AddDays(today, d), service.UpdateOccurrenceRequest{Status: service.StatusCompleted}); err != nil {
			t.Fatalf("complete day %d: %v", d, err)
		}
	}
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

type snapshotError struct{ items []service.ScheduleItem }

func (e *snapshotError) Error() string {
	return "a schedule snapshot had more than one actionable open occurrence"
}
