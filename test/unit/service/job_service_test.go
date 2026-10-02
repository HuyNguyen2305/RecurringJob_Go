package service_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
	"recurringjob/internal/service"
)

type memJobs struct {
	mockJobs
	created []*model.Job
}

func (m *memJobs) Create(_ context.Context, job *model.Job) error {
	job.ID = "new"
	m.created = append(m.created, job)
	m.mockJobs[job.ID] = job
	return nil
}

func newJobService() (*service.JobService, *memJobs) {
	repo := &memJobs{mockJobs: mockJobs{}}
	return service.NewJobService(repo, service.NewOccurrenceResolver(repo)), repo
}

func TestCreateJob(t *testing.T) {
	ctx := context.Background()

	t.Run("one-off job defaults status", func(t *testing.T) {
		s, repo := newJobService()
		job, err := s.CreateJob(ctx, service.CreateJobInput{Date: dt("2026-10-02")})
		if err != nil || job.Status != service.StatusUnconfirmed || job.Recurrence != nil || len(repo.created) != 1 {
			t.Fatalf("job=%+v err=%v", job, err)
		}
	})

	t.Run("recurrence defaults are saved", func(t *testing.T) {
		s, _ := newJobService()
		job, err := s.CreateJob(ctx, service.CreateJobInput{Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "daily"}})
		if err != nil || job.Recurrence.EndsType != "never" || job.Recurrence.Interval != 1 {
			t.Fatalf("job=%+v err=%v", job, err)
		}
	})

	t.Run("rejections save nothing", func(t *testing.T) {
		tests := []struct {
			name   string
			in     service.CreateJobInput
			status int
		}{
			{"rescheduled status", service.CreateJobInput{Date: dt("2026-10-02"), Status: service.StatusRescheduled}, 400},
			{"unknown status", service.CreateJobInput{Date: dt("2026-10-02"), Status: "x"}, 400},
			{"bad recurrence", service.CreateJobInput{Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "weekly"}}, 400},
			{"missing except job", service.CreateJobInput{Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-0000000000ff"}}, 404},
		}
		for _, tt := range tests {
			s, repo := newJobService()
			_, err := s.CreateJob(ctx, tt.in)
			if got := statusOf(t, err); got != tt.status {
				t.Errorf("%s: status %d, want %d", tt.name, got, tt.status)
			}
			if len(repo.created) != 0 {
				t.Errorf("%s: job was saved", tt.name)
			}
		}
	})
}

func TestJobOccurrences(t *testing.T) {
	ctx := context.Background()
	s, repo := newJobService()
	repo.mockJobs["00000000-0000-0000-0000-00000000000f"] = recJob("00000000-0000-0000-0000-00000000000f", "2026-10-02", recurrence.Rule{Frequency: "daily"})

	got, err := s.Occurrences(ctx, "00000000-0000-0000-0000-00000000000f", dt("2026-10-03"), dt("2026-10-05"), 0)
	if want := []string{"2026-10-03", "2026-10-04", "2026-10-05"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v err=%v", got, err)
	}
	got, err = s.Occurrences(ctx, "00000000-0000-0000-0000-00000000000f", dt("2026-10-03"), dt("2026-10-09"), 2)
	if want := []string{"2026-10-03", "2026-10-04"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("limit: got %v err=%v", got, err)
	}
	// Endless with neither "to" nor a limit falls back to the default limit.
	if got, err := s.Occurrences(ctx, "00000000-0000-0000-0000-00000000000f", time.Time{}, time.Time{}, 0); err != nil || len(got) != service.DefaultOccurrencesLimit {
		t.Fatalf("default limit: len=%d err=%v", len(got), err)
	}

	for name, c := range map[string]struct {
		id         string
		from, to   time.Time
		limit      int
		wantStatus int
	}{
		"limit too large": {"00000000-0000-0000-0000-00000000000f", time.Time{}, time.Time{}, 1001, 400},
		"negative limit":  {"00000000-0000-0000-0000-00000000000f", time.Time{}, time.Time{}, -1, 400},
		"from after to":   {"00000000-0000-0000-0000-00000000000f", dt("2026-10-09"), dt("2026-10-01"), 5, 400},
		"unknown job":     {"00000000-0000-0000-0000-0000000000ff", time.Time{}, time.Time{}, 5, 404},
		"malformed id":    {"not-a-uuid", time.Time{}, time.Time{}, 5, 400},
	} {
		_, err := s.Occurrences(ctx, c.id, c.from, c.to, c.limit)
		if got := statusOf(t, err); got != c.wantStatus {
			t.Errorf("%s: status %d, want %d", name, got, c.wantStatus)
		}
	}
}

type failingJobRepo struct {
	faultyJobs
	createErr error
}

func (f failingJobRepo) Create(context.Context, *model.Job) error { return f.createErr }

func TestJobServiceMore(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("db down")

	t.Run("a repository error on save is returned as is", func(t *testing.T) {
		repo := failingJobRepo{faultyJobs: faultyJobs{err: boom}, createErr: boom}
		s := service.NewJobService(repo, service.NewOccurrenceResolver(repo))
		if _, err := s.CreateJob(ctx, service.CreateJobInput{Date: dt("2026-10-02")}); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("an infrastructure error while checking exceptJobId is returned as is", func(t *testing.T) {
		repo := failingJobRepo{faultyJobs: faultyJobs{err: boom}}
		s := service.NewJobService(repo, service.NewOccurrenceResolver(repo))
		in := service.CreateJobInput{Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-0000000000ff"}}
		if _, err := s.CreateJob(ctx, in); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a job lookup error in Occurrences is returned as is", func(t *testing.T) {
		repo := failingJobRepo{faultyJobs: faultyJobs{err: boom}}
		s := service.NewJobService(repo, service.NewOccurrenceResolver(repo))
		if _, err := s.Occurrences(ctx, "00000000-0000-0000-0000-0000000000aa", time.Time{}, time.Time{}, 5); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("the date is truncated to its UTC calendar day", func(t *testing.T) {
		s, repo := newJobService()
		in := time.Date(2026, 10, 3, 2, 30, 0, 0, time.FixedZone("+7", 7*3600)) // 2026-10-02 19:30 UTC
		job, err := s.CreateJob(ctx, service.CreateJobInput{Date: in})
		if err != nil || !job.Date.Equal(dt("2026-10-02")) || job.Date.Location() != time.UTC {
			t.Fatalf("job=%+v err=%v", job, err)
		}
		if len(repo.created) != 1 {
			t.Fatal("not saved")
		}
	})

	t.Run("every creatable status is kept as given", func(t *testing.T) {
		for _, st := range []string{service.StatusUnconfirmed, service.StatusConfirmed, service.StatusInProgress, service.StatusCompleted, service.StatusCanceled, service.StatusTerminateService} {
			s, _ := newJobService()
			job, err := s.CreateJob(ctx, service.CreateJobInput{Date: dt("2026-10-02"), Status: st})
			if err != nil || job.Status != st {
				t.Errorf("%s: job=%+v err=%v", st, job, err)
			}
		}
	})

	t.Run("a valid except-frequency reference is accepted", func(t *testing.T) {
		s, repo := newJobService()
		other := uid("c1")
		repo.mockJobs[other] = oneOff(other, "2026-10-09")
		in := service.CreateJobInput{Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: other}}
		job, err := s.CreateJob(ctx, in)
		if err != nil || job.Recurrence.ExceptJobID != other {
			t.Fatalf("job=%+v err=%v", job, err)
		}
	})
}
