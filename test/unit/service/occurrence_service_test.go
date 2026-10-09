package service_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
	"recurringjob/internal/service"
)

// memOccRepo is an in-memory OccurrenceRepository.
type memOccRepo struct {
	rows      map[string]*model.JobOccurrence
	seq       int
	afterList func()   // runs after ListByJob, to simulate a concurrent writer
	log       []string // Create / UpdateGuarded / LockOccurrence calls, in order
	lockErr   error
}

func newMemOccRepo() *memOccRepo { return &memOccRepo{rows: map[string]*model.JobOccurrence{}} }

func (m *memOccRepo) ListByJob(_ context.Context, jobID string) ([]model.JobOccurrence, error) {
	var out []model.JobOccurrence
	for _, r := range m.rows {
		if r.JobID == jobID {
			out = append(out, *r)
		}
	}
	if m.afterList != nil {
		f := m.afterList
		m.afterList = nil
		f()
	}
	return out, nil
}

func (m *memOccRepo) Create(_ context.Context, occ *model.JobOccurrence) error {
	m.log = append(m.log, "create")
	for _, r := range m.rows {
		if r.JobID == occ.JobID && r.OccurrenceDate.Equal(occ.OccurrenceDate) {
			return apperror.Conflict("occurrence was changed by another request")
		}
	}
	m.seq++
	c := *occ
	c.ID = string(rune('a' + m.seq))
	m.rows[c.ID] = &c
	return nil
}

func (m *memOccRepo) UpdateGuarded(_ context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error) {
	m.log = append(m.log, "update")
	r, ok := m.rows[id]
	if !ok || !contains(allowedFrom, r.Status) {
		return 0, nil
	}
	r.Status = updates["status"].(string)
	if v, ok := updates["completed_at"]; ok {
		t := v.(time.Time)
		r.CompletedAt = &t
	}
	if v, ok := updates["rescheduled_to"]; ok {
		t := v.(time.Time)
		r.RescheduledTo = &t
	}
	return 1, nil
}

func (m *memOccRepo) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	snap := map[string]model.JobOccurrence{}
	for id, r := range m.rows {
		snap[id] = *r
	}
	if err := fn(ctx); err != nil {
		m.rows = map[string]*model.JobOccurrence{}
		for id, r := range snap {
			c := r
			m.rows[id] = &c
		}
		return err
	}
	return nil
}

func (m *memOccRepo) byDate(date string) *model.JobOccurrence {
	for _, r := range m.rows {
		if r.OccurrenceDate.Equal(dt(date)) {
			return r
		}
	}
	return nil
}

var fixedNow = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func newService(jobs mockJobs) (*service.OccurrenceService, *memOccRepo) {
	repo := newMemOccRepo()
	s := service.NewOccurrenceService(jobs, repo, service.NewOccurrenceResolver(jobs))
	s.WithClock(func() time.Time { return fixedNow })
	return s, repo
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var ae *apperror.AppError
	if !errors.As(err, &ae) {
		t.Fatalf("want AppError, got %v", err)
	}
	return ae.Status
}

func upd(status string) service.UpdateOccurrenceRequest {
	return service.UpdateOccurrenceRequest{Status: status}
}

func TestUpdateOccurrence(t *testing.T) {
	ctx := context.Background()
	daily := recJob("00000000-0000-0000-0000-00000000000a", "2026-09-29", recurrence.Rule{Frequency: "daily"})
	daily.Status = service.StatusUnconfirmed
	weeklyFri := recJob("00000000-0000-0000-0000-00000000000b", "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}})
	weeklyFri.Status = service.StatusUnconfirmed
	future := oneOff("00000000-0000-0000-0000-00000000000e", "2026-10-10")
	future.Status = service.StatusUnconfirmed

	t.Run("first change inserts a row; later occurrence is hollow (409)", func(t *testing.T) {
		s, repo := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": daily})
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusConfirmed)); err != nil {
			t.Fatal(err)
		}
		if r := repo.byDate("2026-09-29"); r == nil || r.Status != service.StatusConfirmed {
			t.Fatalf("row not stored: %+v", r)
		}
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-30"), upd(service.StatusCompleted))
		if got := statusOf(t, err); got != 409 {
			t.Fatalf("hollow occurrence: status %d, want 409", got)
		}
	})

	t.Run("completing opens the next occurrence and stamps completed_at", func(t *testing.T) {
		s, repo := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": daily})
		saved, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusCompleted))
		if err != nil || saved.CompletedAt == nil {
			t.Fatalf("saved=%+v err=%v", saved, err)
		}
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-30"), upd(service.StatusConfirmed)); err != nil {
			t.Fatal(err)
		}
		if repo.byDate("2026-09-30") == nil {
			t.Fatal("second row missing")
		}
	})

	t.Run("final occurrence cannot change (409)", func(t *testing.T) {
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": daily})
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusCompleted)); err != nil {
			t.Fatal(err)
		}
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusCanceled))
		if got := statusOf(t, err); got != 409 {
			t.Fatalf("status %d, want 409", got)
		}
	})

	t.Run("disallowed transition (409)", func(t *testing.T) {
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": daily})
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusConfirmed)); err != nil {
			t.Fatal(err)
		}
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusUnconfirmed))
		if got := statusOf(t, err); got != 409 {
			t.Fatalf("status %d, want 409", got)
		}
	})

	t.Run("completed in the future is 400", func(t *testing.T) {
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000e": future})
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000e", dt("2026-10-10"), upd(service.StatusCompleted))
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("status %d, want 400", got)
		}
	})

	t.Run("date that is not an occurrence is 404", func(t *testing.T) {
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000b": weeklyFri, "00000000-0000-0000-0000-00000000000e": future})
		for _, c := range []struct{ id, date string }{{"00000000-0000-0000-0000-00000000000b", "2026-10-03"}, {"00000000-0000-0000-0000-00000000000b", "2026-09-25"}, {"00000000-0000-0000-0000-00000000000e", "2026-10-11"}} {
			_, err := s.UpdateOccurrence(ctx, c.id, dt(c.date), upd(service.StatusConfirmed))
			if got := statusOf(t, err); got != 404 {
				t.Fatalf("%v: status %d, want 404", c, got)
			}
		}
	})

	t.Run("unknown job is 404", func(t *testing.T) {
		s, _ := newService(mockJobs{})
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-0000000000ff", dt("2026-10-02"), upd(service.StatusConfirmed))
		if got := statusOf(t, err); got != 404 {
			t.Fatalf("status %d, want 404", got)
		}
	})

	t.Run("rescheduledTo is required for rescheduled and rejected otherwise (400)", func(t *testing.T) {
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000b": weeklyFri})
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000b", dt("2026-10-02"), upd(service.StatusRescheduled))
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("missing target: status %d", got)
		}
		_, err = s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000b", dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusConfirmed, RescheduledTo: dp("2026-10-05")})
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("unexpected target: status %d", got)
		}
		_, err = s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000b", dt("2026-10-02"), upd("bogus"))
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("unknown status: status %d", got)
		}
	})

	t.Run("reschedule writes both rows and the new visit is available", func(t *testing.T) {
		s, repo := newService(mockJobs{"00000000-0000-0000-0000-00000000000b": weeklyFri})
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000b", dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-05")})
		if err != nil {
			t.Fatal(err)
		}
		orig, visit := repo.byDate("2026-10-02"), repo.byDate("2026-10-05")
		if orig == nil || orig.Status != service.StatusRescheduled || orig.RescheduledTo == nil || !orig.RescheduledTo.Equal(dt("2026-10-05")) {
			t.Fatalf("original row wrong: %+v", orig)
		}
		if visit == nil || visit.Status != service.StatusUnconfirmed || visit.RescheduledFrom == nil || !visit.RescheduledFrom.Equal(dt("2026-10-02")) {
			t.Fatalf("visit row wrong: %+v", visit)
		}
		// A rescheduled-to visit is always available, even though the date is in the future.
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000b", dt("2026-10-05"), upd(service.StatusConfirmed)); err != nil {
			t.Fatal(err)
		}
		// ... and it can be moved again (chain).
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000b", dt("2026-10-05"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-06")}); err != nil {
			t.Fatal(err)
		}
		// The 5th was confirmed before it moved, so the 6th is confirmed too.
		if moved := repo.byDate("2026-10-06"); moved == nil || moved.Status != service.StatusConfirmed {
			t.Fatalf("a rescheduled visit keeps its status: %+v", moved)
		}
		items, err := s.Schedule(ctx, "00000000-0000-0000-0000-00000000000b", service.ScheduleQuery{Limit: 4})
		if err != nil {
			t.Fatal(err)
		}
		want := []brief{{"2026-10-02", service.StateReal, "rescheduled"}, {"2026-10-05", service.StateReal, "rescheduled"}, {"2026-10-06", service.StateReal, "confirmed"}, {"2026-10-09", service.StateHollow, "unconfirmed"}}
		if got := summarize(items); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
			t.Fatalf("got  %v\nwant %v", got, want)
		}
	})

	t.Run("reschedule target rules", func(t *testing.T) {
		ending := recJob("00000000-0000-0000-0000-00000000000c", "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}, EndsType: "on_date", EndsOnDate: "2026-10-09"})
		ending.Status = service.StatusUnconfirmed
		pastJob := recJob("00000000-0000-0000-0000-00000000000d", "2026-09-22", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{2}})
		pastJob.Status = service.StatusUnconfirmed
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000b": weeklyFri, "00000000-0000-0000-0000-00000000000c": ending, "00000000-0000-0000-0000-00000000000d": pastJob})
		cases := []struct {
			name, job, date, to string
			want                int
		}{
			{"not after the date", "00000000-0000-0000-0000-00000000000b", "2026-10-02", "2026-10-02", 400},
			{"in the past", "00000000-0000-0000-0000-00000000000d", "2026-09-22", "2026-09-28", 400},
			{"after the series end", "00000000-0000-0000-0000-00000000000c", "2026-10-02", "2026-10-12", 400},
			{"already a series date", "00000000-0000-0000-0000-00000000000b", "2026-10-02", "2026-10-09", 409},
		}
		for _, c := range cases {
			_, err := s.UpdateOccurrence(ctx, c.job, dt(c.date), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp(c.to)})
			if got := statusOf(t, err); got != c.want {
				t.Errorf("%s: status %d, want %d", c.name, got, c.want)
			}
		}
		// Target with an existing row is a 409.
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000b", dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-12")}); err != nil {
			t.Fatal(err)
		}
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000b", dt("2026-10-09"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-12")})
		if got := statusOf(t, err); got != 409 {
			t.Errorf("existing row at target: status %d, want 409", got)
		}
	})

	t.Run("a one-off job can be rescheduled", func(t *testing.T) {
		s, repo := newService(mockJobs{"00000000-0000-0000-0000-00000000000e": future})
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000e", dt("2026-10-10"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-12")}); err != nil {
			t.Fatal(err)
		}
		if repo.byDate("2026-10-12") == nil {
			t.Fatal("visit missing")
		}
	})

	t.Run("optimistic guard: row changed after read is 409", func(t *testing.T) {
		s, repo := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": daily})
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusConfirmed)); err != nil {
			t.Fatal(err)
		}
		repo.afterList = func() { repo.byDate("2026-09-29").Status = service.StatusCompleted }
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusConfirmed))
		if got := statusOf(t, err); got != 409 {
			t.Fatalf("status %d, want 409", got)
		}
	})

	t.Run("terminate_service ends the series", func(t *testing.T) {
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": daily})
		if _, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-29"), upd(service.StatusTerminateService)); err != nil {
			t.Fatal(err)
		}
		_, err := s.UpdateOccurrence(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-09-30"), upd(service.StatusConfirmed))
		if got := statusOf(t, err); got != 409 {
			t.Fatalf("status %d, want 409", got)
		}
		items, err := s.Schedule(ctx, "00000000-0000-0000-0000-00000000000a", service.ScheduleQuery{Limit: 5})
		if err != nil || len(items) != 1 || items[0].Status != service.StatusTerminateService {
			t.Fatalf("items=%v err=%v", items, err)
		}
	})
}

func TestMalformedJobIDIs400(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(mockJobs{})
	const bad = "not-a-uuid"
	_, err := s.UpdateOccurrence(ctx, bad, dt("2026-10-02"), upd(service.StatusConfirmed))
	checks := map[string]error{
		"UpdateOccurrence":    err,
		"AvailableFor":        s.AvailableFor(ctx, bad, dt("2026-10-02")),
		"ValidOccurrenceDate": s.ValidOccurrenceDate(ctx, bad, dt("2026-10-02")),
	}
	_, checks["Schedule"] = s.Schedule(ctx, bad, service.ScheduleQuery{})
	for name, err := range checks {
		if got := statusOf(t, err); got != 400 {
			t.Errorf("%s: status %d, want 400", name, got)
		}
	}
}

func TestSchedule(t *testing.T) {
	ctx := context.Background()

	t.Run("endless series expands the window until the limit is met", func(t *testing.T) {
		j := recJob("00000000-0000-0000-0000-00000000000a", "2026-10-02", recurrence.Rule{Frequency: "monthly", MonthlyRepeatBy: "day_of_month"})
		j.Status = service.StatusUnconfirmed
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": j})
		items, err := s.Schedule(ctx, "00000000-0000-0000-0000-00000000000a", service.ScheduleQuery{Limit: 4})
		want := []brief{{"2026-10-02", service.StateReal, "unconfirmed"}, {"2026-11-02", service.StateHollow, "unconfirmed"}, {"2026-12-02", service.StateHollow, "unconfirmed"}, {"2027-01-02", service.StateHollow, "unconfirmed"}}
		got := summarize(items)
		if err != nil || len(got) != 4 || got[0] != want[0] || got[3] != want[3] {
			t.Fatalf("got %v err=%v", got, err)
		}
	})

	t.Run("an overdue occurrence stays first; the visits after it are listed but locked", func(t *testing.T) {
		j := recJob("00000000-0000-0000-0000-00000000000a", "2026-09-29", recurrence.Rule{Frequency: "daily"})
		j.Status = service.StatusUnconfirmed
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": j})
		items, err := s.Schedule(ctx, "00000000-0000-0000-0000-00000000000a", service.ScheduleQuery{Limit: 10})
		if err != nil || len(items) != 10 || items[0].State != service.StateOverdue || items[0].Date != "2026-09-29" {
			t.Fatalf("items=%v err=%v", items, err)
		}
		for i, it := range items[1:] {
			if it.State != service.StateHollow || it.Status != service.StatusUnconfirmed {
				t.Fatalf("item %d (%+v) must be hollow and unconfirmed", i+1, it)
			}
		}
	})

	t.Run("from trims output but gating starts at the job date", func(t *testing.T) {
		j := recJob("00000000-0000-0000-0000-00000000000a", "2026-10-02", recurrence.Rule{Frequency: "daily", EndsType: "after", EndsAfterCount: 5})
		j.Status = service.StatusUnconfirmed
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": j})
		items, err := s.Schedule(ctx, "00000000-0000-0000-0000-00000000000a", service.ScheduleQuery{From: dt("2026-10-04"), Limit: 2})
		got := summarize(items)
		if err != nil || len(got) != 2 || got[0] != (brief{"2026-10-04", service.StateHollow, "unconfirmed"}) || got[1].Date != "2026-10-05" {
			t.Fatalf("got %v err=%v", got, err)
		}
	})

	t.Run("to bounds the walk", func(t *testing.T) {
		j := recJob("00000000-0000-0000-0000-00000000000a", "2026-10-02", recurrence.Rule{Frequency: "daily"})
		j.Status = service.StatusUnconfirmed
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": j})
		items, err := s.Schedule(ctx, "00000000-0000-0000-0000-00000000000a", service.ScheduleQuery{To: dt("2026-10-04")})
		if err != nil || len(items) != 3 {
			t.Fatalf("items=%v err=%v", items, err)
		}
	})

	t.Run("limit out of range is 400", func(t *testing.T) {
		j := oneOff("00000000-0000-0000-0000-00000000000a", "2026-10-02")
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": j})
		_, err := s.Schedule(ctx, "00000000-0000-0000-0000-00000000000a", service.ScheduleQuery{Limit: 5000})
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("status %d", got)
		}
	})

	t.Run("one-off job schedule", func(t *testing.T) {
		j := oneOff("00000000-0000-0000-0000-00000000000a", "2026-10-10")
		j.Status = service.StatusConfirmed
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": j})
		items, err := s.Schedule(ctx, "00000000-0000-0000-0000-00000000000a", service.ScheduleQuery{})
		if err != nil || len(items) != 1 || items[0].Status != service.StatusConfirmed || items[0].State != service.StateReal {
			t.Fatalf("items=%v err=%v", items, err)
		}
	})
}

func TestAvailabilityChecks(t *testing.T) {
	ctx := context.Background()
	j := recJob("00000000-0000-0000-0000-00000000000a", "2026-10-02", recurrence.Rule{Frequency: "daily"})
	j.Status = service.StatusUnconfirmed

	t.Run("real ok, hollow 409, invalid 404", func(t *testing.T) {
		s, _ := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": j})
		if err := s.AvailableFor(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-10-02")); err != nil {
			t.Fatal(err)
		}
		if got := statusOf(t, s.AvailableFor(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-10-03"))); got != 409 {
			t.Fatalf("hollow: %d", got)
		}
		if got := statusOf(t, s.ValidOccurrenceDate(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-10-01"))); got != 404 {
			t.Fatalf("invalid: %d", got)
		}
		if err := s.ValidOccurrenceDate(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-10-03")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("canceled, rescheduled and terminated are refused; completed is fine", func(t *testing.T) {
		for status, ok := range map[string]bool{service.StatusCanceled: false, service.StatusRescheduled: false, service.StatusTerminateService: false, service.StatusCompleted: true} {
			s, repo := newService(mockJobs{"00000000-0000-0000-0000-00000000000a": j})
			rr := model.JobOccurrence{JobID: "00000000-0000-0000-0000-00000000000a", OccurrenceDate: dt("2026-10-02"), Status: status}
			if status == service.StatusRescheduled {
				rr.RescheduledTo = dp("2026-10-20")
			}
			_ = repo.Create(ctx, &rr)
			err := s.AvailableFor(ctx, "00000000-0000-0000-0000-00000000000a", dt("2026-10-02"))
			if ok && err != nil {
				t.Errorf("%s: unexpected %v", status, err)
			}
			if !ok && statusOf(t, err) != 409 {
				t.Errorf("%s: want 409", status)
			}
		}
	})
}

func uid(suffix string) string { return "00000000-0000-0000-0000-0000000000" + suffix }

func unconfirmed(j *model.Job) *model.Job { j.Status = service.StatusUnconfirmed; return j }

var allStatuses = []string{service.StatusUnconfirmed, service.StatusConfirmed, service.StatusCompleted, service.StatusCanceled, service.StatusTerminateService, service.StatusRescheduled}

// Every stored status x every requested status, checked against the rules.
func TestUpdateOccurrenceTransitionMatrix(t *testing.T) {
	ctx := context.Background()
	id := uid("a1")
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			t.Run(from+" to "+to, func(t *testing.T) {
				s, repo := newService(mockJobs{id: unconfirmed(oneOff(id, "2026-09-29"))})
				seed := model.JobOccurrence{JobID: id, OccurrenceDate: dt("2026-09-29"), Status: from}
				if from == service.StatusRescheduled {
					seed.RescheduledTo = dp("2026-10-20")
				}
				if err := repo.Create(ctx, &seed); err != nil {
					t.Fatal(err)
				}
				before := len(repo.rows)

				req := service.UpdateOccurrenceRequest{Status: to}
				if to == service.StatusRescheduled {
					req.RescheduledTo = dp("2026-10-20")
				}
				_, err := s.UpdateOccurrence(ctx, id, dt("2026-09-29"), req)

				row := repo.byDate("2026-09-29")
				if !service.IsFinal(from) && service.CanTransition(from, to) {
					if err != nil {
						t.Fatalf("want success, got %v", err)
					}
					wantRows := before
					if to == service.StatusRescheduled {
						wantRows++
					}
					if row.Status != to || len(repo.rows) != wantRows {
						t.Fatalf("row status %q rows %d, want %q and %d", row.Status, len(repo.rows), to, wantRows)
					}
					if to == service.StatusCompleted && row.CompletedAt == nil {
						t.Fatal("completed_at not set")
					}
					if to == service.StatusRescheduled && (row.RescheduledTo == nil || repo.byDate("2026-10-20") == nil) {
						t.Fatal("reschedule rows missing")
					}
					return
				}
				if got := statusOf(t, err); got != 409 {
					t.Fatalf("status %d, want 409 (%v)", got, err)
				}
				if row.Status != from || len(repo.rows) != before {
					t.Fatalf("state changed on a refused update: status %q rows %d", row.Status, len(repo.rows))
				}
			})
		}
	}
}

func TestUpdateOccurrenceUsesEffectiveStatusWhenThereIsNoRow(t *testing.T) {
	ctx := context.Background()
	id := uid("a2")
	for _, jobStatus := range []string{service.StatusUnconfirmed, service.StatusConfirmed, service.StatusCompleted, service.StatusCanceled, service.StatusTerminateService} {
		for _, to := range []string{service.StatusConfirmed, service.StatusCompleted, service.StatusCanceled} {
			t.Run(jobStatus+" job, request "+to, func(t *testing.T) {
				job := oneOff(id, "2026-09-29")
				job.Status = jobStatus
				s, repo := newService(mockJobs{id: job})
				_, err := s.UpdateOccurrence(ctx, id, dt("2026-09-29"), upd(to))
				if service.CanTransition(jobStatus, to) {
					if err != nil || repo.byDate("2026-09-29") == nil {
						t.Fatalf("want success, got %v", err)
					}
					return
				}
				if got := statusOf(t, err); got != 409 {
					t.Fatalf("status %d, want 409", got)
				}
			})
		}
	}

	t.Run("a later occurrence of a confirmed job starts confirmed", func(t *testing.T) {
		job := recJob(id, "2026-09-29", recurrence.Rule{Frequency: "daily"})
		job.Status = service.StatusConfirmed
		s, _ := newService(mockJobs{id: job})
		if _, err := s.UpdateOccurrence(ctx, id, dt("2026-09-29"), upd(service.StatusCompleted)); err != nil {
			t.Fatalf("first occurrence must be completable: %v", err)
		}
		if _, err := s.UpdateOccurrence(ctx, id, dt("2026-09-30"), upd(service.StatusConfirmed)); statusOf(t, err) != 409 {
			t.Fatalf("a later occurrence already is confirmed, so confirming it again is refused: %v", err)
		}
	})
}

func TestUpdateOccurrenceDateBoundaries(t *testing.T) {
	ctx := context.Background()
	id := uid("a3")
	complete := func(date string) error {
		s, _ := newService(mockJobs{id: unconfirmed(oneOff(id, date))})
		_, err := s.UpdateOccurrence(ctx, id, dt(date), upd(service.StatusCompleted))
		return err
	}
	if err := complete("2026-10-01"); err != nil {
		t.Errorf("yesterday: %v", err)
	}
	if err := complete("2026-10-02"); err != nil {
		t.Errorf("today: %v", err)
	}
	if got := statusOf(t, complete("2026-10-03")); got != 400 {
		t.Errorf("tomorrow: status %d, want 400", got)
	}

	reschedule := func(to string) error {
		s, _ := newService(mockJobs{id: unconfirmed(oneOff(id, "2026-09-29"))})
		_, err := s.UpdateOccurrence(ctx, id, dt("2026-09-29"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp(to)})
		return err
	}
	if err := reschedule("2026-10-02"); err != nil {
		t.Errorf("reschedule to today: %v", err)
	}
	if got := statusOf(t, reschedule("2026-10-01")); got != 400 {
		t.Errorf("reschedule to yesterday: status %d, want 400", got)
	}
	if got := statusOf(t, reschedule("2026-09-29")); got != 400 {
		t.Errorf("reschedule to the same day: status %d, want 400", got)
	}
}

func is409(err error) bool {
	var ae *apperror.AppError
	return errors.As(err, &ae) && ae.Status == 409
}

func TestRescheduleAgainstSeriesRules(t *testing.T) {
	ctx := context.Background()
	a, b := uid("1a"), uid("1b")
	move := func(s *service.OccurrenceService, id, date, to string) error {
		_, err := s.UpdateOccurrence(ctx, id, dt(date), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp(to)})
		return err
	}

	t.Run("an except-frequency job may be moved onto a date its exception removed", func(t *testing.T) {
		s, repo := newService(mockJobs{
			a: unconfirmed(recJob(a, "2026-10-02", exceptJob("daily", 1, b))),
			b: unconfirmed(oneOff(b, "2026-10-05")),
		})
		if err := move(s, a, "2026-10-02", "2026-10-05"); err != nil {
			t.Fatal(err)
		}
		if repo.byDate("2026-10-05") == nil {
			t.Fatal("visit row missing")
		}
	})

	t.Run("an except-frequency job cannot be moved onto one of its own dates", func(t *testing.T) {
		s, _ := newService(mockJobs{
			a: unconfirmed(recJob(a, "2026-10-02", exceptJob("daily", 1, b))),
			b: unconfirmed(oneOff(b, "2026-10-05")),
		})
		if err := move(s, a, "2026-10-02", "2026-10-06"); !is409(err) {
			t.Fatalf("want 409, got %v", err)
		}
	})

	weeklyAfter3 := func() mockJobs { // 10-02, 10-09, 10-16
		return mockJobs{a: unconfirmed(recJob(a, "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}, EndsType: "after", EndsAfterCount: 3}))}
	}
	t.Run("finite series: the end is the last scheduled date", func(t *testing.T) {
		s, _ := newService(weeklyAfter3())
		if got := statusOf(t, move(s, a, "2026-10-02", "2026-10-19")); got != 400 {
			t.Fatalf("after the end: status %d, want 400", got)
		}
		s, _ = newService(weeklyAfter3())
		if err := move(s, a, "2026-10-02", "2026-10-16"); !is409(err) {
			t.Fatalf("onto the last series date must be 409, got %v", err)
		}
		s, _ = newService(weeklyAfter3())
		if err := move(s, a, "2026-10-02", "2026-10-15"); err != nil {
			t.Fatalf("the day before the end: %v", err)
		}
	})

	t.Run("endsAfter counts dates removed by except rules when finding the end", func(t *testing.T) {
		jobs := func() mockJobs { // raw dates 09-29 .. 10-03; October is excepted
			return mockJobs{a: unconfirmed(recJob(a, "2026-09-29", recurrence.Rule{Frequency: "daily", EndsType: "after", EndsAfterCount: 5, ExceptType: "month", ExceptMonths: []int{10}}))}
		}
		s, _ := newService(jobs())
		if got := statusOf(t, move(s, a, "2026-09-29", "2026-10-04")); got != 400 {
			t.Fatalf("after the raw end: status %d, want 400", got)
		}
		s, _ = newService(jobs())
		if err := move(s, a, "2026-09-29", "2026-10-03"); err != nil {
			t.Fatalf("onto the raw end (excepted, so free): %v", err)
		}
	})

	t.Run("on_date series end", func(t *testing.T) {
		jobs := func() mockJobs {
			return mockJobs{a: unconfirmed(recJob(a, "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}, EndsType: "on_date", EndsOnDate: "2026-10-09"}))}
		}
		s, _ := newService(jobs())
		if got := statusOf(t, move(s, a, "2026-10-02", "2026-10-10")); got != 400 {
			t.Fatalf("status %d, want 400", got)
		}
		s, _ = newService(jobs())
		if err := move(s, a, "2026-10-02", "2026-10-08"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("corrupt stored rule is a 400, not a crash", func(t *testing.T) {
		s, _ := newService(mockJobs{a: unconfirmed(recJob(a, "2026-10-02", recurrence.Rule{Frequency: "daily", EndsType: "on_date", EndsOnDate: "garbage"}))})
		_, err := s.UpdateOccurrence(ctx, a, dt("2026-10-02"), upd(service.StatusConfirmed))
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("status %d, want 400", got)
		}
	})
}

// Infrastructure failures must reach the caller untouched (they become 500s).
type faultyJobs struct{ err error }

func (f faultyJobs) GetJob(context.Context, string) (*model.Job, error) { return nil, f.err }

type faultyOccRepo struct {
	*memOccRepo
	listErr, updateErr, txErr error
	failCreateOn              int // fail the n-th Create call (1-based); 0 = never
	creates                   int
	createErr                 error
}

func (f *faultyOccRepo) ListByJob(ctx context.Context, id string) ([]model.JobOccurrence, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.memOccRepo.ListByJob(ctx, id)
}

func (f *faultyOccRepo) Create(ctx context.Context, o *model.JobOccurrence) error {
	f.creates++
	if f.failCreateOn != 0 && f.creates == f.failCreateOn {
		return f.createErr
	}
	return f.memOccRepo.Create(ctx, o)
}

func (f *faultyOccRepo) UpdateGuarded(ctx context.Context, id string, from []string, u map[string]any) (int64, error) {
	if f.updateErr != nil {
		return 0, f.updateErr
	}
	return f.memOccRepo.UpdateGuarded(ctx, id, from, u)
}

func (f *faultyOccRepo) Transaction(ctx context.Context, fn func(context.Context) error) error {
	if f.txErr != nil {
		return f.txErr
	}
	return f.memOccRepo.Transaction(ctx, fn)
}

func TestInfrastructureErrorsPropagate(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down")
	id := uid("a4")
	date := dt("2026-09-29")
	jobs := mockJobs{id: unconfirmed(oneOff(id, "2026-09-29"))}
	build := func(f *faultyOccRepo) *service.OccurrenceService {
		f.memOccRepo = newMemOccRepo()
		f.createErr = boom
		s := service.NewOccurrenceService(jobs, f, service.NewOccurrenceResolver(jobs))
		s.WithClock(func() time.Time { return fixedNow })
		return s
	}
	rawBoom := func(t *testing.T, err error) {
		t.Helper()
		var ae *apperror.AppError
		if !errors.Is(err, boom) || errors.As(err, &ae) {
			t.Fatalf("want the raw infrastructure error, got %v", err)
		}
	}

	t.Run("job lookup fails", func(t *testing.T) {
		fj := faultyJobs{err: boom}
		s := service.NewOccurrenceService(fj, newMemOccRepo(), service.NewOccurrenceResolver(fj))
		_, err := s.UpdateOccurrence(ctx, id, date, upd(service.StatusConfirmed))
		rawBoom(t, err)
		_, err = s.Schedule(ctx, id, service.ScheduleQuery{})
		rawBoom(t, err)
		rawBoom(t, s.AvailableFor(ctx, id, date))
		rawBoom(t, s.ValidOccurrenceDate(ctx, id, date))
	})
	t.Run("listing occurrences fails", func(t *testing.T) {
		s := build(&faultyOccRepo{listErr: boom})
		_, err := s.UpdateOccurrence(ctx, id, date, upd(service.StatusConfirmed))
		rawBoom(t, err)
		_, err = s.Schedule(ctx, id, service.ScheduleQuery{})
		rawBoom(t, err)
		rawBoom(t, s.AvailableFor(ctx, id, date))
	})
	t.Run("insert fails", func(t *testing.T) {
		f := &faultyOccRepo{failCreateOn: 1}
		s := build(f)
		_, err := s.UpdateOccurrence(ctx, id, date, upd(service.StatusConfirmed))
		rawBoom(t, err)
		if len(f.rows) != 0 {
			t.Fatal("a row was written")
		}
	})
	t.Run("guarded update fails", func(t *testing.T) {
		f := &faultyOccRepo{}
		s := build(f)
		_ = f.memOccRepo.Create(ctx, &model.JobOccurrence{JobID: id, OccurrenceDate: date, Status: service.StatusConfirmed})
		f.updateErr = boom
		_, err := s.UpdateOccurrence(ctx, id, date, upd(service.StatusCompleted))
		rawBoom(t, err)
	})
	t.Run("transaction fails", func(t *testing.T) {
		s := build(&faultyOccRepo{txErr: boom})
		_, err := s.UpdateOccurrence(ctx, id, date, upd(service.StatusConfirmed))
		rawBoom(t, err)
	})
	t.Run("the second insert of a reschedule fails and nothing is left behind", func(t *testing.T) {
		f := &faultyOccRepo{failCreateOn: 2}
		s := build(f)
		_, err := s.UpdateOccurrence(ctx, id, date, service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-20")})
		rawBoom(t, err)
		if len(f.rows) != 0 {
			t.Fatalf("%d rows left after a failed reschedule", len(f.rows))
		}
	})
}

func TestScheduleMore(t *testing.T) {
	ctx := context.Background()
	id := uid("a5")
	weekly := func() *model.Job {
		return unconfirmed(recJob(id, "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}}))
	}

	t.Run("limit bounds", func(t *testing.T) {
		s, _ := newService(mockJobs{id: unconfirmed(oneOff(id, "2026-10-10"))})
		for limit, ok := range map[int]bool{0: true, 1: true, 1000: true, -1: false, 1001: false, -1000: false} {
			_, err := s.Schedule(ctx, id, service.ScheduleQuery{Limit: limit})
			if ok && err != nil {
				t.Errorf("limit %d: %v", limit, err)
			}
			if !ok && statusOf(t, err) != 400 {
				t.Errorf("limit %d: want 400", limit)
			}
		}
	})

	t.Run("limit 1 returns exactly one item", func(t *testing.T) {
		s, _ := newService(mockJobs{id: weekly()})
		items, err := s.Schedule(ctx, id, service.ScheduleQuery{Limit: 1})
		if err != nil || len(items) != 1 || items[0].Date != "2026-10-02" {
			t.Fatalf("items=%v err=%v", items, err)
		}
	})

	t.Run("from after to is a 400, like /occurrences", func(t *testing.T) {
		s, _ := newService(mockJobs{id: weekly()})
		_, err := s.Schedule(ctx, id, service.ScheduleQuery{From: dt("2026-10-20"), To: dt("2026-10-10")})
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("status %d, want 400", got)
		}
		// The check runs before the job lookup, so an unknown job is still a 400.
		_, err = s.Schedule(ctx, uid("ff"), service.ScheduleQuery{From: dt("2026-10-20"), To: dt("2026-10-10")})
		if got := statusOf(t, err); got != 400 {
			t.Fatalf("unknown job with bad dates: status %d, want 400", got)
		}
	})

	t.Run("from equal to to, or only one of them, is fine", func(t *testing.T) {
		s, _ := newService(mockJobs{id: weekly()})
		for name, q := range map[string]service.ScheduleQuery{
			"from == to": {From: dt("2026-10-09"), To: dt("2026-10-09")},
			"only from":  {From: dt("2026-10-20")},
			"only to":    {To: dt("2026-10-10")},
		} {
			if _, err := s.Schedule(ctx, id, q); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
		items, _ := s.Schedule(ctx, id, service.ScheduleQuery{From: dt("2026-10-09"), To: dt("2026-10-09")})
		if len(items) != 1 || items[0].Date != "2026-10-09" {
			t.Errorf("from == to should return that single day: %v", items)
		}
	})

	t.Run("from and to window, gating still starts at the job date", func(t *testing.T) {
		s, _ := newService(mockJobs{id: weekly()})
		items, err := s.Schedule(ctx, id, service.ScheduleQuery{From: dt("2026-10-09"), To: dt("2026-10-23")})
		got := summarize(items)
		want := []brief{{"2026-10-09", service.StateHollow, "unconfirmed"}, {"2026-10-16", service.StateHollow, "unconfirmed"}, {"2026-10-23", service.StateHollow, "unconfirmed"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v err=%v", got, err)
		}
	})

	t.Run("an endless yearly series stops at the 2099 horizon", func(t *testing.T) {
		job := unconfirmed(recJob(id, "2026-10-02", recurrence.Rule{Frequency: "yearly", YearlyRepeatBy: "day_of_year"}))
		s, _ := newService(mockJobs{id: job})
		items, err := s.Schedule(ctx, id, service.ScheduleQuery{Limit: 1000})
		if err != nil || len(items) != 74 {
			t.Fatalf("len=%d err=%v", len(items), err)
		}
		if items[0].State != service.StateReal || items[1].State != service.StateHollow || items[73].Date != "2099-10-02" {
			t.Fatalf("first=%+v second=%+v last=%+v", items[0], items[1], items[73])
		}
	})

	t.Run("rescheduled-to visits are valid and available; the original and later dates are not", func(t *testing.T) {
		s, _ := newService(mockJobs{id: weekly()})
		if _, err := s.UpdateOccurrence(ctx, id, dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-05")}); err != nil {
			t.Fatal(err)
		}
		if err := s.ValidOccurrenceDate(ctx, id, dt("2026-10-05")); err != nil {
			t.Fatalf("a rescheduled-to date is a valid occurrence: %v", err)
		}
		if err := s.AvailableFor(ctx, id, dt("2026-10-05")); err != nil {
			t.Fatalf("a rescheduled-to visit is always available: %v", err)
		}
		if got := statusOf(t, s.AvailableFor(ctx, id, dt("2026-10-02"))); got != 409 {
			t.Fatalf("the rescheduled original must be refused, got %d", got)
		}
		if got := statusOf(t, s.AvailableFor(ctx, id, dt("2026-10-09"))); got != 409 {
			t.Fatalf("the date after an open visit is hollow, got %d", got)
		}
	})

	t.Run("an except-frequency job's schedule honours the exception", func(t *testing.T) {
		a, b := uid("2a"), uid("2b")
		s, _ := newService(mockJobs{
			a: unconfirmed(recJob(a, "2026-10-02", exceptJob("daily", 1, b))),
			b: unconfirmed(oneOff(b, "2026-10-03")),
		})
		items, err := s.Schedule(ctx, a, service.ScheduleQuery{Limit: 3})
		got := summarize(items)
		want := []brief{{"2026-10-02", service.StateReal, "unconfirmed"}, {"2026-10-04", service.StateHollow, "unconfirmed"}, {"2026-10-05", service.StateHollow, "unconfirmed"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v err=%v", got, err)
		}
	})
}

// flakyJobs fails the failOn-th GetJob call, to reach error paths that only
// show up when a later lookup in the same request fails.
type flakyJobs struct {
	mockJobs
	failOn int
	calls  *int
	err    error
}

func (f flakyJobs) GetJob(ctx context.Context, id string) (*model.Job, error) {
	*f.calls++
	if *f.calls == f.failOn {
		return nil, f.err
	}
	return f.mockJobs.GetJob(ctx, id)
}

func TestLaterLookupFailuresPropagate(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down")
	a, b := uid("3a"), uid("3b")
	for failOn, name := range map[int]string{
		2: "first resolve in locate",
		3: "second resolve in locate",
		4: "target check of a reschedule",
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			jobs := flakyJobs{
				mockJobs: mockJobs{a: unconfirmed(recJob(a, "2026-10-02", exceptJob("daily", 1, b))), b: oneOff(b, "2026-10-20")},
				failOn:   failOn, calls: &calls, err: boom,
			}
			s := service.NewOccurrenceService(jobs, newMemOccRepo(), service.NewOccurrenceResolver(jobs))
			s.WithClock(func() time.Time { return fixedNow })
			_, err := s.UpdateOccurrence(ctx, a, dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-06")})
			if !errors.Is(err, boom) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestScheduleErrorAndTrimPaths(t *testing.T) {
	ctx := context.Background()
	id := uid("a6")
	corrupt := func(rule recurrence.Rule) *service.OccurrenceService {
		s, _ := newService(mockJobs{id: unconfirmed(recJob(id, "2026-10-02", rule))})
		return s
	}

	t.Run("a corrupt rule is a 400 in every schedule branch", func(t *testing.T) {
		cases := map[string]struct {
			rule recurrence.Rule
			q    service.ScheduleQuery
		}{
			"explicit to":    {recurrence.Rule{Frequency: "hourly"}, service.ScheduleQuery{To: dt("2026-12-31")}},
			"finite series":  {recurrence.Rule{Frequency: "hourly", EndsType: "after", EndsAfterCount: 3}, service.ScheduleQuery{}},
			"endless series": {recurrence.Rule{Frequency: "hourly"}, service.ScheduleQuery{}},
		}
		for name, c := range cases {
			_, err := corrupt(c.rule).Schedule(ctx, id, c.q)
			if got := statusOf(t, err); got != 400 {
				t.Errorf("%s: status %d, want 400", name, got)
			}
		}
	})

	t.Run("a rescheduled visit beyond To is trimmed from the output", func(t *testing.T) {
		weekly := unconfirmed(recJob(id, "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}}))
		s, _ := newService(mockJobs{id: weekly})
		if _, err := s.UpdateOccurrence(ctx, id, dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: dp("2026-10-12")}); err != nil {
			t.Fatal(err)
		}
		items, err := s.Schedule(ctx, id, service.ScheduleQuery{To: dt("2026-10-09")})
		got := summarize(items)
		// 10-12 (the visit the original moved to) lies beyond To and is trimmed;
		// 10-09 is hollow because the visit is still open.
		want := []brief{{"2026-10-02", service.StateReal, "rescheduled"}, {"2026-10-09", service.StateHollow, "unconfirmed"}}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %v err=%v\nwant %v", got, err, want)
		}
	})

	t.Run("AvailableFor on a date that is not an occurrence is a 404", func(t *testing.T) {
		s, _ := newService(mockJobs{id: unconfirmed(recJob(id, "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}}))})
		if got := statusOf(t, s.AvailableFor(ctx, id, dt("2026-10-03"))); got != 404 {
			t.Fatalf("status %d, want 404", got)
		}
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestScheduleHorizon(t *testing.T) {
	tests := []struct{ job, want string }{
		{"2026-10-02", "2099-12-31"}, // ordinary jobs keep the spec horizon
		{"2089-12-31", "2099-12-31"},
		{"2090-01-01", "2100-01-01"}, // the later of the two wins
		{"2099-12-31", "2109-12-31"},
		{"2100-03-01", "2110-03-01"},
		{"2096-02-29", "2106-03-01"}, // 2106 has no Feb 29; AddDate normalises forward
		{"9990-01-01", "9999-12-31"}, // clamped to the last representable year
		{"9999-12-30", "9999-12-31"},
	}
	for _, tt := range tests {
		got := service.ScheduleHorizon(dt(tt.job))
		if got.Format("2006-01-02") != tt.want {
			t.Errorf("ScheduleHorizon(%s) = %s, want %s", tt.job, got.Format("2006-01-02"), tt.want)
		}
	}
}

func TestScheduleBeyondTheDefaultHorizon(t *testing.T) {
	ctx := context.Background()
	const id = "00000000-0000-0000-0000-00000000000a"

	t.Run("a one-off in 2100 is scheduled", func(t *testing.T) {
		j := oneOff(id, "2100-03-01")
		j.Status = service.StatusUnconfirmed
		s, _ := newService(mockJobs{id: j})
		items, err := s.Schedule(ctx, id, service.ScheduleQuery{})
		if err != nil || len(items) != 1 || items[0].Date != "2100-03-01" {
			t.Fatalf("items=%v err=%v", items, err)
		}
	})

	t.Run("an endless daily series in 2100 honours the limit", func(t *testing.T) {
		j := recJob(id, "2100-03-01", recurrence.Rule{Frequency: "daily"})
		j.Status = service.StatusUnconfirmed
		s, _ := newService(mockJobs{id: j})
		items, err := s.Schedule(ctx, id, service.ScheduleQuery{Limit: 5})
		if err != nil || len(items) != 5 || items[0].Date != "2100-03-01" || items[4].Date != "2100-03-05" {
			t.Fatalf("items=%v err=%v", summarize(items), err)
		}
	})

	t.Run("a finite series in 9999 is scheduled up to the end of the calendar", func(t *testing.T) {
		j := recJob(id, "9999-12-30", recurrence.Rule{Frequency: "daily", EndsType: "after", EndsAfterCount: 5})
		j.Status = service.StatusUnconfirmed
		s, _ := newService(mockJobs{id: j})
		items, err := s.Schedule(ctx, id, service.ScheduleQuery{})
		if err != nil || len(items) != 2 || items[1].Date != "9999-12-31" {
			t.Fatalf("items=%v err=%v", summarize(items), err)
		}
	})
}

func TestCompletedAtIsStoredAtMicrosecondPrecision(t *testing.T) {
	const id = "00000000-0000-0000-0000-00000000000a"
	j := recJob(id, "2026-10-02", recurrence.Rule{Frequency: "daily"})
	j.Status = service.StatusUnconfirmed
	s, repo := newService(mockJobs{id: j})
	s.WithClock(func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 123456789, time.UTC) })

	saved, err := s.UpdateOccurrence(context.Background(), id, dt("2026-10-02"), upd(service.StatusCompleted))
	if err != nil || saved.CompletedAt == nil {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	want := time.Date(2026, 10, 2, 12, 0, 0, 123456000, time.UTC)
	if !saved.CompletedAt.Equal(want) {
		t.Errorf("response completedAt = %s, want %s", saved.CompletedAt.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
	if row := repo.byDate("2026-10-02"); row == nil || row.CompletedAt == nil || !row.CompletedAt.Equal(want) {
		t.Errorf("stored completedAt = %v, want %s", row, want)
	}
}

// fakeInvoices records what the occurrence service asks of the invoice side.
type fakeInvoices struct {
	voided  []voidCall
	moved   []moveCall
	paid    []string
	voidErr error
	moveErr error
	paidErr error
}

func (f *fakeInvoices) MoveUnpaidForOccurrence(_ context.Context, jobID string, from, to time.Time) (int64, error) {
	f.moved = append(f.moved, moveCall{jobID, from, to})
	return 1, f.moveErr
}

func (f *fakeInvoices) VoidUnpaidForOccurrence(_ context.Context, jobID string, date time.Time) (int64, error) {
	f.voided = append(f.voided, voidCall{jobID: jobID, date: date})
	return 1, f.voidErr
}

func (f *fakeInvoices) PaidIDsForOccurrence(context.Context, string, time.Time) ([]string, error) {
	return f.paid, f.paidErr
}

func TestCancelingOrTerminatingAnOccurrenceVoidsItsInvoices(t *testing.T) {
	ctx := context.Background()
	jobID := uid("b1")
	newJob := func() mockJobs {
		job := recJob(jobID, "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}})
		job.Status = service.StatusUnconfirmed
		return mockJobs{jobID: job}
	}

	for _, status := range []string{service.StatusCanceled, service.StatusTerminateService} {
		t.Run(status+" voids the unpaid invoices of that occurrence", func(t *testing.T) {
			s, repo := newService(newJob())
			voider := &fakeInvoices{}
			s.WithInvoices(voider)
			saved, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(status))
			if err != nil {
				t.Fatal(err)
			}
			if len(voider.voided) != 1 || voider.voided[0].jobID != jobID || !voider.voided[0].date.Equal(dt("2026-10-02")) {
				t.Fatalf("void calls %+v", voider.voided)
			}
			if len(saved.PaidInvoiceIDs) != 0 || repo.byDate("2026-10-02") == nil || repo.byDate("2026-10-02").Status != status {
				t.Fatalf("saved %+v", saved)
			}
		})

		t.Run(status+" reports the paid invoices it kept", func(t *testing.T) {
			s, _ := newService(newJob())
			s.WithInvoices(&fakeInvoices{paid: []string{"inv-1", "inv-2"}})
			saved, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(status))
			if err != nil || !reflect.DeepEqual(saved.PaidInvoiceIDs, []string{"inv-1", "inv-2"}) {
				t.Fatalf("saved %+v err=%v", saved, err)
			}
		})
	}

	t.Run("other status changes leave invoices alone", func(t *testing.T) {
		for _, status := range []string{service.StatusConfirmed, service.StatusCompleted} {
			s, _ := newService(newJob())
			voider := &fakeInvoices{paid: []string{"inv-1"}}
			s.WithInvoices(voider)
			saved, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(status))
			if err != nil || len(voider.voided) != 0 || len(saved.PaidInvoiceIDs) != 0 {
				t.Errorf("%s: err=%v voided=%v saved=%+v", status, err, voider.voided, saved)
			}
		}
	})

	t.Run("rescheduling moves the unpaid invoices and reports the paid ones it left", func(t *testing.T) {
		s, repo := newService(newJob())
		inv := &fakeInvoices{paid: []string{"inv-paid"}}
		s.WithInvoices(inv)
		to := dt("2026-10-03")
		saved, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: &to})
		if err != nil {
			t.Fatal(err)
		}
		if len(inv.voided) != 0 {
			t.Fatalf("a reschedule must not void: %+v", inv.voided)
		}
		if len(inv.moved) != 1 || inv.moved[0].jobID != jobID || !inv.moved[0].from.Equal(dt("2026-10-02")) || !inv.moved[0].to.Equal(to) {
			t.Fatalf("move calls %+v", inv.moved)
		}
		if !reflect.DeepEqual(saved.PaidInvoiceIDs, []string{"inv-paid"}) || repo.byDate("2026-10-03") == nil {
			t.Fatalf("saved %+v", saved)
		}
	})

	t.Run("a failure while moving rolls the reschedule back", func(t *testing.T) {
		boom := errors.New("db down")
		for name, inv := range map[string]*fakeInvoices{
			"move fails":   {moveErr: boom},
			"lookup fails": {paidErr: boom},
		} {
			s, repo := newService(newJob())
			s.WithInvoices(inv)
			to := dt("2026-10-03")
			if _, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: &to}); !errors.Is(err, boom) {
				t.Errorf("%s: %v", name, err)
			}
			if repo.byDate("2026-10-02") != nil || repo.byDate("2026-10-03") != nil {
				t.Errorf("%s: the reschedule stayed although its invoices were not handled", name)
			}
		}
	})

	t.Run("a failure while voiding rolls the status change back", func(t *testing.T) {
		boom := errors.New("db down")
		for name, voider := range map[string]*fakeInvoices{
			"void fails":   {voidErr: boom},
			"lookup fails": {paidErr: boom},
		} {
			s, repo := newService(newJob())
			s.WithInvoices(voider)
			if _, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(service.StatusCanceled)); !errors.Is(err, boom) {
				t.Errorf("%s: %v", name, err)
			}
			if repo.byDate("2026-10-02") != nil {
				t.Errorf("%s: the occurrence stayed canceled although its invoices were not handled", name)
			}
		}
	})
}

// fakeWorkOrders records what the occurrence service asks of the work order side.
type fakeWorkOrders struct {
	canceled                   []voidCall
	moved                      []moveCall
	kept                       []string // answers a question about completed / in-progress work orders
	open                       []string // answers a question that includes the draft status
	cancelErr, moveErr, idsErr error
	idsAsked                   [][]string
}

func (f *fakeWorkOrders) CancelOpenForOccurrence(_ context.Context, jobID string, date time.Time) (int64, error) {
	f.canceled = append(f.canceled, voidCall{jobID: jobID, date: date})
	return 1, f.cancelErr
}

func (f *fakeWorkOrders) MoveOpenForOccurrence(_ context.Context, jobID string, from, to time.Time) (int64, error) {
	f.moved = append(f.moved, moveCall{jobID, from, to})
	return 1, f.moveErr
}

func (f *fakeWorkOrders) IDsForOccurrence(_ context.Context, _ string, _ time.Time, statuses []string) ([]string, error) {
	f.idsAsked = append(f.idsAsked, statuses)
	if slices.Contains(statuses, service.WOStatusDraft) {
		return f.open, f.idsErr
	}
	return f.kept, f.idsErr
}

func TestAnOccurrenceChangeCarriesItsWorkOrdersAlong(t *testing.T) {
	ctx := context.Background()
	jobID := uid("b3")
	newJob := func() mockJobs {
		job := recJob(jobID, "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}})
		job.Status = service.StatusUnconfirmed
		return mockJobs{jobID: job}
	}

	for _, status := range []string{service.StatusCanceled, service.StatusTerminateService} {
		t.Run(status+" cancels the open work orders and reports the completed ones", func(t *testing.T) {
			s, _ := newService(newJob())
			wo := &fakeWorkOrders{kept: []string{"wo-done"}}
			s.WithWorkOrders(wo)
			saved, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(status))
			if err != nil {
				t.Fatal(err)
			}
			if len(wo.canceled) != 1 || wo.canceled[0].jobID != jobID || !wo.canceled[0].date.Equal(dt("2026-10-02")) || len(wo.moved) != 0 {
				t.Fatalf("calls canceled=%+v moved=%+v", wo.canceled, wo.moved)
			}
			if !reflect.DeepEqual(wo.idsAsked, [][]string{{service.WOStatusCompleted}}) || !reflect.DeepEqual(saved.KeptWorkOrderIDs, []string{"wo-done"}) {
				t.Fatalf("asked %v saved %+v", wo.idsAsked, saved)
			}
		})
	}

	t.Run("rescheduling moves the work orders that have not started and reports the rest", func(t *testing.T) {
		s, repo := newService(newJob())
		wo := &fakeWorkOrders{kept: []string{"wo-busy", "wo-done"}}
		s.WithWorkOrders(wo)
		to := dt("2026-10-03")
		saved, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: &to})
		if err != nil {
			t.Fatal(err)
		}
		if len(wo.canceled) != 0 || len(wo.moved) != 1 || !wo.moved[0].from.Equal(dt("2026-10-02")) || !wo.moved[0].to.Equal(to) {
			t.Fatalf("calls canceled=%+v moved=%+v", wo.canceled, wo.moved)
		}
		if !reflect.DeepEqual(wo.idsAsked, [][]string{{service.WOStatusInProgress, service.WOStatusCompleted}}) ||
			!reflect.DeepEqual(saved.KeptWorkOrderIDs, []string{"wo-busy", "wo-done"}) || repo.byDate("2026-10-03") == nil {
			t.Fatalf("asked %v saved %+v", wo.idsAsked, saved)
		}
	})

	t.Run("confirming leaves work orders alone", func(t *testing.T) {
		s, _ := newService(newJob())
		wo := &fakeWorkOrders{kept: []string{"wo-1"}, open: []string{"wo-2"}}
		s.WithWorkOrders(wo)
		saved, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(service.StatusConfirmed))
		if err != nil || len(wo.canceled)+len(wo.moved)+len(wo.idsAsked) != 0 || len(saved.KeptWorkOrderIDs) != 0 {
			t.Errorf("err=%v wo=%+v saved=%+v", err, wo, saved)
		}
	})

	t.Run("completing is refused while the visit has open work orders", func(t *testing.T) {
		s, repo := newService(newJob())
		wo := &fakeWorkOrders{open: []string{"wo-draft", "wo-busy"}}
		s.WithWorkOrders(wo)
		_, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(service.StatusCompleted))
		if statusOf(t, err) != 409 || !strings.Contains(err.Error(), "wo-draft") || !strings.Contains(err.Error(), "wo-busy") {
			t.Fatalf("err=%v", err)
		}
		if !reflect.DeepEqual(wo.idsAsked, [][]string{{service.WOStatusDraft, service.WOStatusScheduled, service.WOStatusInProgress}}) {
			t.Errorf("asked for %v", wo.idsAsked)
		}
		if repo.byDate("2026-10-02") != nil || len(wo.canceled)+len(wo.moved) != 0 {
			t.Error("a refused completion changed something")
		}
	})

	t.Run("completing goes through once the work orders are done or canceled, and touches none", func(t *testing.T) {
		s, repo := newService(newJob())
		wo := &fakeWorkOrders{kept: []string{"wo-done"}} // only a completed one is left
		s.WithWorkOrders(wo)
		saved, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(service.StatusCompleted))
		if err != nil || repo.byDate("2026-10-02") == nil || repo.byDate("2026-10-02").Status != service.StatusCompleted {
			t.Fatalf("err=%v saved=%+v", err, saved)
		}
		if len(wo.canceled)+len(wo.moved) != 0 || len(saved.KeptWorkOrderIDs) != 0 {
			t.Errorf("work orders were touched: %+v %+v", wo, saved)
		}
	})

	t.Run("a failing lookup stops the completion", func(t *testing.T) {
		boom := errors.New("db down")
		s, repo := newService(newJob())
		s.WithWorkOrders(&fakeWorkOrders{idsErr: boom})
		if _, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(service.StatusCompleted)); !errors.Is(err, boom) || repo.byDate("2026-10-02") != nil {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("a failure rolls the occurrence change back", func(t *testing.T) {
		boom := errors.New("db down")
		to := dt("2026-10-03")
		reschedule := service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: &to}
		for name, c := range map[string]struct {
			wo  *fakeWorkOrders
			req service.UpdateOccurrenceRequest
		}{
			"cancel fails":      {&fakeWorkOrders{cancelErr: boom}, upd(service.StatusCanceled)},
			"lookup fails":      {&fakeWorkOrders{idsErr: boom}, upd(service.StatusCanceled)},
			"move fails":        {&fakeWorkOrders{moveErr: boom}, reschedule},
			"reschedule lookup": {&fakeWorkOrders{idsErr: boom}, reschedule},
		} {
			s, repo := newService(newJob())
			s.WithWorkOrders(c.wo)
			if _, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), c.req); !errors.Is(err, boom) {
				t.Errorf("%s: %v", name, err)
			}
			if repo.byDate("2026-10-02") != nil || repo.byDate("2026-10-03") != nil {
				t.Errorf("%s: the change stayed although its work orders were not handled", name)
			}
		}
	})
}

func TestOccurrencesUseTheTenantsToday(t *testing.T) {
	ctx := context.Background()
	id := uid("a4")
	// fixedNow is 2026-10-02 12:00Z; the tenant's calendar differs from UTC's.
	complete := func(today fakeToday, date string) error {
		s, _ := newService(mockJobs{id: unconfirmed(oneOff(id, date))})
		_, err := s.WithToday(today).UpdateOccurrence(ctx, id, dt(date), upd(service.StatusCompleted))
		return err
	}
	// UTC+14: it is already the 3rd, so the 3rd can be completed.
	if err := complete(fakeToday{day: dt("2026-10-03")}, "2026-10-03"); err != nil {
		t.Errorf("local today ahead of UTC: %v", err)
	}
	// UTC-8 early morning: still the 1st, so the 2nd is the future.
	if got := statusOf(t, complete(fakeToday{day: dt("2026-10-01")}, "2026-10-02")); got != 400 {
		t.Errorf("UTC today but local tomorrow: status %d, want 400", got)
	}

	boom := errors.New("boom")
	if err := complete(fakeToday{err: boom}, "2026-10-01"); !errors.Is(err, boom) {
		t.Errorf("update, provider failure: %v", err)
	}
	s, _ := newService(mockJobs{id: unconfirmed(oneOff(id, "2026-10-01"))})
	s.WithToday(fakeToday{err: boom})
	if _, err := s.Schedule(ctx, id, service.ScheduleQuery{Limit: 10}); !errors.Is(err, boom) {
		t.Errorf("schedule, provider failure: %v", err)
	}
	if err := s.AvailableFor(ctx, id, dt("2026-10-01")); !errors.Is(err, boom) {
		t.Errorf("availability, provider failure: %v", err)
	}
}

func (m *memOccRepo) LockOccurrence(_ context.Context, _ string, date time.Time) error {
	m.log = append(m.log, "lock:"+date.Format("2006-01-02"))
	return m.lockErr
}

func TestOccurrenceChangesTakeTheOccurrenceLockFirst(t *testing.T) {
	ctx := context.Background()
	jobID := uid("b2")
	newJob := func() mockJobs {
		job := recJob(jobID, "2026-10-02", recurrence.Rule{Frequency: "weekly", WeeklyPeriod: "every", WeeklyDaysOfWeek: []int{5}})
		job.Status = service.StatusUnconfirmed
		return mockJobs{jobID: job}
	}
	to := dt("2026-10-03")
	tests := []struct {
		name string
		req  service.UpdateOccurrenceRequest
	}{
		{"confirmed", upd(service.StatusConfirmed)},
		{"completed", upd(service.StatusCompleted)},
		{"canceled", upd(service.StatusCanceled)},
		{"terminate_service", upd(service.StatusTerminateService)},
		{"rescheduled", service.UpdateOccurrenceRequest{Status: service.StatusRescheduled, RescheduledTo: &to}},
	}
	for _, tt := range tests {
		t.Run(tt.name+": the lock for the changed date is the first thing in the transaction", func(t *testing.T) {
			s, repo := newService(newJob())
			s.WithInvoices(&fakeInvoices{})
			if _, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), tt.req); err != nil {
				t.Fatal(err)
			}
			if len(repo.log) < 2 || repo.log[0] != "lock:2026-10-02" {
				t.Fatalf("calls %v", repo.log)
			}
		})
	}

	t.Run("a failing lock changes nothing", func(t *testing.T) {
		boom := errors.New("lock timeout")
		s, repo := newService(newJob())
		inv := &fakeInvoices{}
		s.WithInvoices(inv)
		repo.lockErr = boom
		if _, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd(service.StatusCanceled)); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
		if repo.byDate("2026-10-02") != nil || len(inv.voided) != 0 {
			t.Fatal("the occurrence or its invoices changed although the lock failed")
		}
	})

	t.Run("rejected changes never take the lock", func(t *testing.T) {
		s, repo := newService(newJob())
		if _, err := s.UpdateOccurrence(ctx, jobID, dt("2026-10-02"), upd("nonsense")); err == nil {
			t.Fatal("expected an error")
		}
		if len(repo.log) != 0 {
			t.Fatalf("calls %v", repo.log)
		}
	})
}
