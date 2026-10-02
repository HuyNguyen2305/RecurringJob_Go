package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/internal/service"
	"recurringjob/test/fixtures"
	"recurringjob/test/helpers"
)

func build(db *gorm.DB, wrap func(*repository.OccurrenceRepository) service.OccurrenceRepository) *service.OccurrenceService {
	jobs := repository.NewJobRepository(db)
	occs := repository.NewOccurrenceRepository(db)
	var repo service.OccurrenceRepository = occs
	if wrap != nil {
		repo = wrap(occs)
	}
	return service.NewOccurrenceService(jobs, repo, service.NewOccurrenceResolver(jobs))
}

func appStatus(err error) int {
	var ae *apperror.AppError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

func TestIntegrationReschedule(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	svc := build(db, nil)
	today := civil.Today()

	t.Run("writes the original and the new visit together", func(t *testing.T) {
		job := helpers.SeedJob(t, ctx, db, fixtures.OneOffJob(civil.AddDays(today, 3)))
		to := civil.AddDays(today, 5)
		if _, err := svc.UpdateOccurrence(ctx, job.ID, job.Date, service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: &to}); err != nil {
			t.Fatal(err)
		}
		if n := helpers.CountOccurrences(t, ctx, db, job.ID); n != 2 {
			t.Fatalf("rows = %d, want 2", n)
		}
	})

	t.Run("rolls back the original when the new visit cannot be written", func(t *testing.T) {
		job := helpers.SeedJob(t, ctx, db, fixtures.OneOffJob(civil.AddDays(today, 3)))
		to := civil.AddDays(today, 5)
		sabotaged := build(db, func(r *repository.OccurrenceRepository) service.OccurrenceRepository {
			return &sabotage{OccurrenceRepository: r}
		})
		_, err := sabotaged.UpdateOccurrence(ctx, job.ID, job.Date, service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: &to})
		if got := appStatus(err); got != 409 {
			t.Fatalf("status %d (%v), want 409", got, err)
		}
		// The sabotage row was written in the same transaction, so nothing may remain.
		if n := helpers.CountOccurrences(t, ctx, db, job.ID); n != 0 {
			t.Fatalf("rows = %d, want 0 after rollback", n)
		}
	})
}

// sabotage inserts a conflicting row for the new visit just before the real
// insert, inside the same transaction, forcing a unique violation.
type sabotage struct {
	*repository.OccurrenceRepository
}

func (s *sabotage) Create(ctx context.Context, occ *model.JobOccurrence) error {
	if occ.RescheduledFrom != nil {
		clash := *occ
		clash.ID, clash.RescheduledFrom = "", nil
		if err := s.OccurrenceRepository.Create(ctx, &clash); err != nil {
			return err
		}
	}
	return s.OccurrenceRepository.Create(ctx, occ)
}

func TestIntegrationConcurrentUpdate(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	svc := build(db, nil)
	today := civil.Today()

	race := func(t *testing.T, id string, date time.Time, status string) (ok, conflict int) {
		t.Helper()
		const workers = 4
		var wg sync.WaitGroup
		start := make(chan struct{})
		results := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := svc.UpdateOccurrence(ctx, id, date, service.UpdateOccurrenceRequest{Status: status})
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			switch {
			case err == nil:
				ok++
			case appStatus(err) == 409:
				conflict++
			default:
				t.Fatalf("unexpected error: %v", err)
			}
		}
		return ok, conflict
	}

	t.Run("first change: exactly one insert wins", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			job := helpers.SeedJob(t, ctx, db, fixtures.OneOffJob(today))
			ok, conflict := race(t, job.ID, job.Date, service.StatusCompleted)
			if ok != 1 || conflict != 3 {
				t.Fatalf("round %d: ok=%d conflict=%d, want 1 and 3", i, ok, conflict)
			}
			if n := helpers.CountOccurrences(t, ctx, db, job.ID); n != 1 {
				t.Fatalf("rows = %d, want 1", n)
			}
		}
	})

	t.Run("existing row: exactly one guarded update wins", func(t *testing.T) {
		for i := 0; i < 10; i++ {
			job := helpers.SeedJob(t, ctx, db, fixtures.OneOffJob(today))
			if _, err := svc.UpdateOccurrence(ctx, job.ID, job.Date, service.UpdateOccurrenceRequest{Status: service.StatusConfirmed}); err != nil {
				t.Fatal(err)
			}
			ok, conflict := race(t, job.ID, job.Date, service.StatusCompleted)
			if ok != 1 || conflict != 3 {
				t.Fatalf("round %d: ok=%d conflict=%d, want 1 and 3", i, ok, conflict)
			}
		}
	})
}
