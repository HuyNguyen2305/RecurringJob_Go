package repository_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/auth"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/test/fixtures"
	"recurringjob/test/helpers"
)

func searchPath(t *testing.T, base repository.BaseRepository, ctx context.Context) string {
	t.Helper()
	var sp string
	err := base.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Raw(`SHOW search_path`).Scan(&sp).Error })
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

func TestBaseRepositorySearchPath(t *testing.T) {
	db := helpers.Connect(t)
	schema, ctx := helpers.NewSchema(t, db)
	base := repository.NewBaseRepository(db)

	t.Run("WithSchema scopes the transaction to the tenant schema", func(t *testing.T) {
		sp := searchPath(t, base, ctx)
		// Postgres reports the quoted form: "t_abc...", public
		if !strings.HasPrefix(sp, `"`+schema+`"`) || !strings.Contains(sp, "public") {
			t.Fatalf("search_path = %q, want it to start with %q", sp, `"`+schema+`"`)
		}
	})

	t.Run("the setting does not leak to other connections or later calls", func(t *testing.T) {
		_ = searchPath(t, base, ctx)
		for i := 0; i < 10; i++ { // pooled connections are reused; none may keep the tenant path
			if sp := searchPath(t, base, context.Background()); strings.Contains(sp, schema) {
				t.Fatalf("tenant search_path leaked: %q", sp)
			}
		}
	})

	t.Run("no tenant in the context leaves the default search_path", func(t *testing.T) {
		if sp := searchPath(t, base, context.Background()); strings.Contains(sp, "t_") {
			t.Fatalf("unexpected tenant path %q", sp)
		}
	})

	t.Run("invalid schema names are a 400 and the callback never runs", func(t *testing.T) {
		for _, bad := range []string{"Bad", "bad-name", "1abc", "a;drop schema public", `a"b`, "has space", strings.Repeat("a", 64), "UPPER"} {
			called := false
			err := base.WithSchema(auth.WithTenantSchema(context.Background(), bad), func(tx *gorm.DB) error { called = true; return nil })
			var ae *apperror.AppError
			if !errors.As(err, &ae) || ae.Status != 400 || called {
				t.Errorf("%q: err=%v called=%v", bad, err, called)
			}
		}
	})

	t.Run("a cancelled context stops the call before the callback", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		called := false
		err := base.Transaction(cctx, func(context.Context) error { called = true; return nil })
		if err == nil || called {
			t.Fatalf("err=%v called=%v", err, called)
		}
	})

	t.Run("a callback error is returned unchanged", func(t *testing.T) {
		boom := errors.New("boom")
		if err := base.WithSchema(ctx, func(*gorm.DB) error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestBaseRepositoryTenantIsolation(t *testing.T) {
	db := helpers.Connect(t)
	_, ctxA := helpers.NewSchema(t, db)
	_, ctxB := helpers.NewSchema(t, db)
	jobs := repository.NewJobRepository(db)

	a := fixtures.OneOffJob(civil.New(2026, 10, 2))
	if err := jobs.Create(ctxA, withRefs(t, ctxA, db, a)); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.GetJob(ctxA, a.ID); err != nil {
		t.Fatalf("tenant A cannot read its own job: %v", err)
	}
	_, err := jobs.GetJob(ctxB, a.ID)
	var ae *apperror.AppError
	if !errors.As(err, &ae) || ae.Status != 404 {
		t.Fatalf("tenant B must not see tenant A's job: %v", err)
	}
	if n := helpers.Scalar(t, ctxB, db, `SELECT count(*) FROM jobs`); n != 0 {
		t.Fatalf("tenant B has %d jobs", n)
	}
	if n := helpers.Scalar(t, ctxA, db, `SELECT count(*) FROM jobs`); n != 1 {
		t.Fatalf("tenant A has %d jobs", n)
	}
}

func TestBaseRepositoryNestedTransactions(t *testing.T) {
	db := helpers.Connect(t)
	_, ctx := helpers.NewSchema(t, db)
	base := repository.NewBaseRepository(db)
	jobs := repository.NewJobRepository(db)
	boom := errors.New("boom")
	count := func() int64 { return helpers.Scalar(t, ctx, db, `SELECT count(*) FROM jobs`) }

	t.Run("an inner call joins the outer transaction", func(t *testing.T) {
		j := fixtures.OneOffJob(civil.New(2026, 10, 2))
		err := base.Transaction(ctx, func(txCtx context.Context) error {
			if err := jobs.Create(txCtx, withRefs(t, ctx, db, j)); err != nil {
				return err
			}
			if _, err := jobs.GetJob(txCtx, j.ID); err != nil {
				t.Errorf("inner call cannot see the outer transaction's write: %v", err)
			}
			// A call that does NOT carry the transaction is another connection: it must not see uncommitted data.
			if _, err := jobs.GetJob(ctx, j.ID); err == nil {
				t.Error("an uncommitted job was visible outside its transaction")
			}
			return nil
		})
		if err != nil || count() != 1 {
			t.Fatalf("err=%v count=%d", err, count())
		}
	})

	t.Run("an inner failure rolls back the outer writes", func(t *testing.T) {
		before := count()
		err := base.Transaction(ctx, func(txCtx context.Context) error {
			if err := jobs.Create(txCtx, withRefs(t, ctx, db, fixtures.OneOffJob(civil.New(2026, 10, 3)))); err != nil {
				return err
			}
			return base.Transaction(txCtx, func(context.Context) error { return boom })
		})
		if !errors.Is(err, boom) || count() != before {
			t.Fatalf("err=%v count %d -> %d", err, before, count())
		}
	})

	t.Run("an outer failure after inner success rolls everything back", func(t *testing.T) {
		before := count()
		err := base.Transaction(ctx, func(txCtx context.Context) error {
			if err := base.Transaction(txCtx, func(inner context.Context) error {
				return jobs.Create(inner, withRefs(t, ctx, db, fixtures.OneOffJob(civil.New(2026, 10, 4))))
			}); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) || count() != before {
			t.Fatalf("err=%v count %d -> %d", err, before, count())
		}
	})

	t.Run("a repository error inside a transaction aborts it", func(t *testing.T) {
		before := count()
		err := base.Transaction(ctx, func(txCtx context.Context) error {
			if err := jobs.Create(txCtx, withRefs(t, ctx, db, fixtures.OneOffJob(civil.New(2026, 10, 5)))); err != nil {
				return err
			}
			return jobs.Create(txCtx, withRefs(t, ctx, db, &model.Job{Date: civil.New(2026, 10, 6), Status: "rescheduled"})) // CHECK violation
		})
		if err == nil || count() != before {
			t.Fatalf("err=%v count %d -> %d", err, before, count())
		}
	})
}

func TestRepositoriesReportDatabaseFailures(t *testing.T) {
	admin := helpers.Connect(t)
	_, ctx := helpers.NewSchema(t, admin)
	dead := helpers.Connect(t)
	sqlDB, err := dead.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()

	jobs := repository.NewJobRepository(dead)
	occs := repository.NewOccurrenceRepository(dead)
	notApp := func(name string, err error) {
		t.Helper()
		var ae *apperror.AppError
		if err == nil || errors.As(err, &ae) {
			t.Errorf("%s: want a raw infrastructure error, got %v", name, err)
		}
	}
	_, err = jobs.GetJob(ctx, "00000000-0000-0000-0000-0000000000ff")
	notApp("GetJob", err) // in particular: not mistaken for "not found"
	notApp("Job Create", jobs.Create(ctx, fixtures.OneOffJob(civil.New(2026, 10, 2))))
	_, err = occs.ListByJob(ctx, "00000000-0000-0000-0000-0000000000ff")
	notApp("ListByJob", err)
	notApp("Occurrence Create", occs.Create(ctx, &model.JobOccurrence{JobID: "00000000-0000-0000-0000-0000000000ff", OccurrenceDate: civil.New(2026, 10, 2), Status: "confirmed"}))
	_, err = occs.UpdateGuarded(ctx, "00000000-0000-0000-0000-0000000000ff", []string{"unconfirmed"}, map[string]any{"status": "confirmed"})
	notApp("UpdateGuarded", err)
	notApp("Transaction", occs.Transaction(ctx, func(context.Context) error { return nil }))
}

func TestLockOccurrence(t *testing.T) {
	db := helpers.Connect(t)
	_, ctx := helpers.NewSchema(t, db)
	_, otherCtx := helpers.NewSchema(t, db)
	base := repository.NewBaseRepository(db)
	job := "00000000-0000-0000-0000-0000000000a1"
	d5, d6 := civil.New(2026, 10, 5), civil.New(2026, 10, 6)

	// tryLock runs a transaction that takes the lock and reports whether it
	// got it within the wait; the transaction ends when release is closed.
	tryLock := func(ctx context.Context, jobID string, date time.Time, wait time.Duration) (got bool, release func(), done <-chan error) {
		acquired, stop, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			finished <- base.Transaction(ctx, func(ctx context.Context) error {
				if err := base.LockOccurrence(ctx, jobID, date); err != nil {
					return err
				}
				close(acquired)
				<-stop
				return nil
			})
		}()
		select {
		case <-acquired:
			got = true
		case <-time.After(wait):
		case err := <-finished:
			t.Fatalf("lock transaction ended early: %v", err)
		}
		return got, func() { close(stop) }, finished
	}

	first, releaseFirst, firstDone := tryLock(ctx, job, d5, 5*time.Second)
	if !first {
		t.Fatal("the first transaction could not take a free lock")
	}

	t.Run("a second transaction on the same occurrence waits for the first", func(t *testing.T) {
		got, release, done := tryLock(ctx, job, d5, 400*time.Millisecond)
		if got {
			release()
			t.Fatal("the lock was granted while another transaction held it")
		}
		releaseFirst()
		if err := <-firstDone; err != nil {
			t.Fatal(err)
		}
		// Now the waiting transaction gets it, and finishes once released.
		release()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("another date, another job and another tenant are not blocked", func(t *testing.T) {
		held, releaseHeld, heldDone := tryLock(ctx, job, d5, 5*time.Second)
		if !held {
			t.Fatal("could not take the lock")
		}
		defer func() {
			releaseHeld()
			<-heldDone
		}()
		for name, args := range map[string]struct {
			ctx  context.Context
			job  string
			date time.Time
		}{
			"other date":   {ctx, job, d6},
			"other job":    {ctx, "00000000-0000-0000-0000-0000000000a2", d5},
			"other tenant": {otherCtx, job, d5},
		} {
			got, release, done := tryLock(args.ctx, args.job, args.date, 5*time.Second)
			if !got {
				t.Errorf("%s: blocked by an unrelated lock", name)
			}
			release()
			<-done
		}
	})

	t.Run("the lock is released when the transaction rolls back", func(t *testing.T) {
		boom := errors.New("boom")
		err := base.Transaction(ctx, func(ctx context.Context) error {
			if err := base.LockOccurrence(ctx, job, d5); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatal(err)
		}
		got, release, done := tryLock(ctx, job, d5, 5*time.Second)
		if !got {
			t.Fatal("the lock stayed held after a rollback")
		}
		release()
		<-done
	})
}
