package service_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
	"recurringjob/internal/service"
)

func recJob(id, date string, rule recurrence.Rule) *model.Job {
	d, _ := civil.Parse(date)
	return &model.Job{ID: id, Date: d, Recurrence: &rule}
}

func oneOff(id, date string) *model.Job {
	d, _ := civil.Parse(date)
	return &model.Job{ID: id, Date: d}
}

func exceptJob(freq string, interval int, otherID string) recurrence.Rule {
	return recurrence.Rule{Frequency: freq, Interval: interval, ExceptType: recurrence.ExceptFrequency, ExceptJobID: otherID}
}

func TestResolveOccurrences(t *testing.T) {
	ctx := context.Background()

	t.Run("one-off inside and outside window", func(t *testing.T) {
		r := service.NewOccurrenceResolver(mockJobs{})
		j := oneOff("a", "2026-10-05")
		got, _ := r.ResolveOccurrences(ctx, j, service.Window{From: civil.New(2026, 10, 1), To: civil.New(2026, 10, 9)}, nil)
		if !reflect.DeepEqual(got, []string{"2026-10-05"}) {
			t.Fatalf("got %v", got)
		}
		got, _ = r.ResolveOccurrences(ctx, j, service.Window{From: civil.New(2026, 10, 6)}, nil)
		if len(got) != 0 {
			t.Fatalf("want empty, got %v", got)
		}
		got, _ = r.ResolveOccurrences(ctx, j, service.Window{To: civil.New(2026, 10, 4)}, nil)
		if len(got) != 0 {
			t.Fatalf("want empty, got %v", got)
		}
	})

	t.Run("non-frequency rule goes straight to the engine", func(t *testing.T) {
		r := service.NewOccurrenceResolver(mockJobs{})
		j := recJob("a", "2026-10-01", recurrence.Rule{Frequency: "daily"})
		got, err := r.ResolveOccurrences(ctx, j, service.Window{Limit: 3}, nil)
		if err != nil || !reflect.DeepEqual(got, []string{"2026-10-01", "2026-10-02", "2026-10-03"}) {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("engine validation error maps to 400", func(t *testing.T) {
		r := service.NewOccurrenceResolver(mockJobs{})
		j := recJob("a", "2026-10-01", recurrence.Rule{Frequency: "daily"})
		if _, err := r.ResolveOccurrences(ctx, j, service.Window{}, nil); err == nil {
			t.Fatal("endless rule without to/limit should error")
		}
	})

	t.Run("except frequency against a one-off job", func(t *testing.T) {
		b := oneOff("b", "2026-10-03")
		a := recJob("a", "2026-10-01", exceptJob("daily", 1, "b"))
		r := service.NewOccurrenceResolver(mockJobs{"a": a, "b": b})
		got, err := r.ResolveOccurrences(ctx, a, service.Window{Limit: 3}, nil)
		if err != nil || !reflect.DeepEqual(got, []string{"2026-10-01", "2026-10-02", "2026-10-04"}) {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("chain A->B->C", func(t *testing.T) {
		c := oneOff("c", "2026-10-03")
		b := recJob("b", "2026-10-01", exceptJob("daily", 2, "c")) // 01,05,07,09... (03 removed by C)
		a := recJob("a", "2026-10-01", exceptJob("daily", 1, "b"))
		r := service.NewOccurrenceResolver(mockJobs{"a": a, "b": b, "c": c})
		got, err := r.ResolveOccurrences(ctx, a, service.Window{Limit: 4}, nil)
		want := []string{"2026-10-02", "2026-10-03", "2026-10-04", "2026-10-06"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, %v\nwant %v", got, err, want)
		}
	})

	t.Run("cycle A<->B terminates", func(t *testing.T) {
		a := recJob("a", "2026-10-01", exceptJob("daily", 1, "b"))
		b := recJob("b", "2026-10-01", exceptJob("daily", 2, "a"))
		r := service.NewOccurrenceResolver(mockJobs{"a": a, "b": b})
		got, err := r.ResolveOccurrences(ctx, a, service.Window{Limit: 4}, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Inside the cycle, A's exclusion list is ignored, so B (01,03) is
		// fully covered by A and drops out; A keeps all four dates.
		want := []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v\nwant %v", got, want)
		}
	})

	t.Run("widening stops at the candidate cap", func(t *testing.T) {
		// B is the same daily series as A, so every candidate is excluded.
		a := recJob("a", "2026-10-01", exceptJob("daily", 1, "b"))
		b := recJob("b", "2026-10-01", recurrence.Rule{Frequency: "daily"})
		r := service.NewOccurrenceResolver(mockJobs{"a": a, "b": b})
		got, err := r.ResolveOccurrences(ctx, a, service.Window{Limit: 5}, nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})

	t.Run("widening fills a limit past excluded dates", func(t *testing.T) {
		// B excludes the first 4 days; limit 3 needs a wider raw window.
		b := recJob("b", "2026-10-01", recurrence.Rule{Frequency: "daily", EndsType: "after", EndsAfterCount: 4})
		a := recJob("a", "2026-10-01", exceptJob("daily", 1, "b"))
		r := service.NewOccurrenceResolver(mockJobs{"a": a, "b": b})
		got, err := r.ResolveOccurrences(ctx, a, service.Window{Limit: 3}, nil)
		want := []string{"2026-10-05", "2026-10-06", "2026-10-07"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, %v\nwant %v", got, err, want)
		}
	})

	t.Run("missing referenced job is 404", func(t *testing.T) {
		a := recJob("a", "2026-10-01", exceptJob("daily", 1, "nope"))
		r := service.NewOccurrenceResolver(mockJobs{"a": a})
		if _, err := r.ResolveOccurrences(ctx, a, service.Window{Limit: 3}, nil); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestResolveOccurrencesMore(t *testing.T) {
	ctx := context.Background()
	after := func(n int) recurrence.Rule {
		return recurrence.Rule{Frequency: "daily", EndsType: "after", EndsAfterCount: n}
	}
	resolve := func(t *testing.T, jobs mockJobs, id string, w service.Window, visited map[string]struct{}) []string {
		t.Helper()
		got, err := service.NewOccurrenceResolver(jobs).ResolveOccurrences(ctx, jobs[id], w, visited)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return got
	}

	t.Run("widening doubles repeatedly until the limit is met", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-10-01", exceptJob("daily", 1, "b")), "b": recJob("b", "2026-10-01", after(20))}
		got := resolve(t, jobs, "a", service.Window{Limit: 3}, nil)
		if want := []string{"2026-10-21", "2026-10-22", "2026-10-23"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("excess candidates are trimmed to the requested limit", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-10-01", exceptJob("daily", 1, "b")), "b": recJob("b", "2026-10-01", after(2))}
		got := resolve(t, jobs, "a", service.Window{Limit: 3}, nil)
		if want := []string{"2026-10-03", "2026-10-04", "2026-10-05"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("nothing excluded: the first pass already satisfies the limit", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-10-01", exceptJob("daily", 1, "b")), "b": oneOff("b", "2027-01-01")}
		got := resolve(t, jobs, "a", service.Window{Limit: 3}, nil)
		if want := []string{"2026-10-01", "2026-10-02", "2026-10-03"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("a limit above MaxExceptCandidates is clamped", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-01-01", exceptJob("daily", 1, "b")), "b": oneOff("b", "2999-01-01")}
		got := resolve(t, jobs, "a", service.Window{Limit: 20000}, nil)
		if len(got) != service.MaxExceptCandidates {
			t.Fatalf("len = %d, want %d", len(got), service.MaxExceptCandidates)
		}
	})

	t.Run("unlimited limit on a finite series", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-10-01", func() recurrence.Rule {
			r := exceptJob("daily", 1, "b")
			r.EndsType, r.EndsAfterCount = "after", 5
			return r
		}()), "b": oneOff("b", "2026-10-03")}
		got := resolve(t, jobs, "a", service.Window{Limit: recurrence.Unlimited}, nil)
		if want := []string{"2026-10-01", "2026-10-02", "2026-10-04", "2026-10-05"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("a job already in visited is resolved without its exception", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-10-01", exceptJob("daily", 1, "b")), "b": oneOff("b", "2026-10-02")}
		got := resolve(t, jobs, "a", service.Window{Limit: 3}, map[string]struct{}{"a": {}})
		if want := []string{"2026-10-01", "2026-10-02", "2026-10-03"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("from/to window with an exception", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-10-01", exceptJob("daily", 1, "b")), "b": oneOff("b", "2026-10-06")}
		got := resolve(t, jobs, "a", service.Window{From: dt("2026-10-05"), To: dt("2026-10-07")}, nil)
		if want := []string{"2026-10-05", "2026-10-07"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("a three-way cycle terminates", func(t *testing.T) {
		jobs := mockJobs{
			"a": recJob("a", "2026-10-01", exceptJob("daily", 1, "b")),
			"b": recJob("b", "2026-10-01", exceptJob("daily", 2, "c")),
			"c": recJob("c", "2026-10-01", exceptJob("daily", 3, "a")),
		}
		for _, id := range []string{"a", "b", "c"} {
			if got := resolve(t, jobs, id, service.Window{Limit: 5}, nil); len(got) > 5 {
				t.Fatalf("%s: %d results", id, len(got))
			}
		}
	})

	t.Run("a job that excepts itself terminates", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-10-01", exceptJob("daily", 1, "a"))}
		if got := resolve(t, jobs, "a", service.Window{Limit: 3}, nil); len(got) > 3 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("infrastructure error from the job lookup is not swallowed", func(t *testing.T) {
		boom := errors.New("db down")
		a := recJob("a", "2026-10-01", exceptJob("daily", 1, "b"))
		_, err := service.NewOccurrenceResolver(faultyJobs{err: boom}).ResolveOccurrences(ctx, a, service.Window{Limit: 3}, nil)
		if !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("an invalid rule on the referenced job is a 400", func(t *testing.T) {
		jobs := mockJobs{"a": recJob("a", "2026-10-01", exceptJob("daily", 1, "b")), "b": recJob("b", "2026-10-01", recurrence.Rule{Frequency: "weekly"})}
		_, err := service.NewOccurrenceResolver(jobs).ResolveOccurrences(ctx, jobs["a"], service.Window{Limit: 3}, nil)
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("status %d, want 400", got)
		}
	})

	t.Run("one-off job: window edges are inclusive", func(t *testing.T) {
		jobs := mockJobs{"x": oneOff("x", "2026-10-05")}
		for _, w := range []service.Window{{From: dt("2026-10-05")}, {To: dt("2026-10-05")}, {From: dt("2026-10-05"), To: dt("2026-10-05")}, {}} {
			if got := resolve(t, jobs, "x", w, nil); len(got) != 1 {
				t.Errorf("window %+v: %v", w, got)
			}
		}
		if got := resolve(t, jobs, "x", service.Window{From: dt("2026-10-06")}, nil); len(got) != 0 {
			t.Errorf("after: %v", got)
		}
	})
}

func TestResolveOccurrencesFrequencyEdges(t *testing.T) {
	ctx := context.Background()

	t.Run("an invalid rule on the job itself is a 400 even with except-frequency", func(t *testing.T) {
		bad := exceptJob("weekly", 1, "b") // weekly without weekdays
		jobs := mockJobs{"a": recJob("a", "2026-10-01", bad), "b": oneOff("b", "2026-10-02")}
		_, err := service.NewOccurrenceResolver(jobs).ResolveOccurrences(ctx, jobs["a"], service.Window{Limit: 3}, nil)
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("status %d, want 400", got)
		}
	})

	t.Run("the referenced job is looked up first, so a missing one is a 404 even when the window is empty", func(t *testing.T) {
		rule := exceptJob("daily", 1, "b")
		rule.EndsType, rule.EndsAfterCount = "after", 2
		jobs := mockJobs{"a": recJob("a", "2026-10-01", rule)} // "b" does not exist
		_, err := service.NewOccurrenceResolver(jobs).ResolveOccurrences(ctx, jobs["a"], service.Window{From: dt("2027-01-01"), Limit: 3}, nil)
		if got := statusOf(t, err); got != 404 {
			t.Fatalf("status %d, want 404", got)
		}
	})

	t.Run("a finite series that ended before the window resolves to nothing", func(t *testing.T) {
		rule := exceptJob("daily", 1, "b")
		rule.EndsType, rule.EndsAfterCount = "after", 2
		jobs := mockJobs{"a": recJob("a", "2026-10-01", rule), "b": oneOff("b", "2026-10-01")}
		got, err := service.NewOccurrenceResolver(jobs).ResolveOccurrences(ctx, jobs["a"], service.Window{From: dt("2027-01-01"), Limit: 3}, nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("got %v, %v", got, err)
		}
	})
}
