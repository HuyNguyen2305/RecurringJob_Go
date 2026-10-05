package service_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"recurringjob/internal/common/apperror"
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
	return service.NewJobService(repo, service.NewOccurrenceResolver(repo), &fakeRefs{}), repo
}

func TestCreateJob(t *testing.T) {
	ctx := context.Background()

	t.Run("one-off job defaults status", func(t *testing.T) {
		s, repo := newJobService()
		job, err := s.CreateJob(ctx, service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02")})
		if err != nil || job.Status != service.StatusUnconfirmed || job.Recurrence != nil || len(repo.created) != 1 {
			t.Fatalf("job=%+v err=%v", job, err)
		}
	})

	t.Run("recurrence defaults are saved", func(t *testing.T) {
		s, _ := newJobService()
		job, err := s.CreateJob(ctx, service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "daily"}})
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
			{"rescheduled status", service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02"), Status: service.StatusRescheduled}, 400},
			{"unknown status", service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02"), Status: "x"}, 400},
			{"bad recurrence", service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "weekly"}}, 400},
			{"missing except job", service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-0000000000ff"}}, 404},
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
		s := service.NewJobService(repo, service.NewOccurrenceResolver(repo), &fakeRefs{})
		if _, err := s.CreateJob(ctx, service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02")}); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("an infrastructure error while checking exceptJobId is returned as is", func(t *testing.T) {
		repo := failingJobRepo{faultyJobs: faultyJobs{err: boom}}
		s := service.NewJobService(repo, service.NewOccurrenceResolver(repo), &fakeRefs{})
		in := service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: "00000000-0000-0000-0000-0000000000ff"}}
		if _, err := s.CreateJob(ctx, in); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a job lookup error in Occurrences is returned as is", func(t *testing.T) {
		repo := failingJobRepo{faultyJobs: faultyJobs{err: boom}}
		s := service.NewJobService(repo, service.NewOccurrenceResolver(repo), &fakeRefs{})
		if _, err := s.Occurrences(ctx, "00000000-0000-0000-0000-0000000000aa", time.Time{}, time.Time{}, 5); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("the date is truncated to its UTC calendar day", func(t *testing.T) {
		s, repo := newJobService()
		in := time.Date(2026, 10, 3, 2, 30, 0, 0, time.FixedZone("+7", 7*3600)) // 2026-10-02 19:30 UTC
		job, err := s.CreateJob(ctx, service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: in})
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
			job, err := s.CreateJob(ctx, service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02"), Status: st})
			if err != nil || job.Status != st {
				t.Errorf("%s: job=%+v err=%v", st, job, err)
			}
		}
	})

	t.Run("a valid except-frequency reference is accepted", func(t *testing.T) {
		s, repo := newJobService()
		other := uid("c1")
		repo.mockJobs[other] = oneOff(other, "2026-10-09")
		in := service.CreateJobInput{CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService, StartTime: "09:00", LengthMinutes: 60, Date: dt("2026-10-02"), Recurrence: &recurrence.Rule{Frequency: "daily", ExceptType: "frequency", ExceptJobID: other}}
		job, err := s.CreateJob(ctx, in)
		if err != nil || job.Recurrence.ExceptJobID != other {
			t.Fatalf("job=%+v err=%v", job, err)
		}
	})
}

func TestCreateJobStartTimeAndLength(t *testing.T) {
	ctx := context.Background()
	in := func(start string, length int) service.CreateJobInput {
		return service.CreateJobInput{
			CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService,
			Date: dt("2026-10-02"), StartTime: start, LengthMinutes: length,
		}
	}

	t.Run("a start time is stored as HH:MM:SS", func(t *testing.T) {
		for given, want := range map[string]string{"09:00": "09:00:00", "09:00:00": "09:00:00", "00:00": "00:00:00", "23:59": "23:59:00", "13:05:30": "13:05:30"} {
			s, _ := newJobService()
			job, err := s.CreateJob(ctx, in(given, 60))
			if err != nil || job.StartTime != want {
				t.Errorf("%q: job=%+v err=%v, want %s", given, job, err, want)
			}
		}
	})

	t.Run("bad start times and lengths are 400s and save nothing", func(t *testing.T) {
		tests := []struct {
			name   string
			start  string
			length int
		}{
			{"empty start", "", 60},
			{"no minutes", "9", 60},
			{"hour 24", "24:00", 60},
			{"minute 60", "09:60", 60},
			{"am/pm", "9am", 60},
			{"leading space", " 09:00", 60},
			{"date instead of time", "2026-10-02", 60},
			{"zero length", "09:00", 0},
			{"negative length", "09:00", -5},
			{"longer than a day", "09:00", service.MaxLengthMinutes + 1},
		}
		for _, tt := range tests {
			s, repo := newJobService()
			if _, err := s.CreateJob(ctx, in(tt.start, tt.length)); statusOf(t, err) != 400 {
				t.Errorf("%s: %v", tt.name, err)
			}
			if len(repo.created) != 0 {
				t.Errorf("%s: the job was saved", tt.name)
			}
		}
	})

	t.Run("the length limits are inclusive", func(t *testing.T) {
		for _, length := range []int{1, service.MaxLengthMinutes} {
			s, _ := newJobService()
			if _, err := s.CreateJob(ctx, in("09:00", length)); err != nil {
				t.Errorf("length %d: %v", length, err)
			}
		}
	})
}

func TestCreateJobReferences(t *testing.T) {
	ctx := context.Background()
	in := service.CreateJobInput{
		CustomerID: refCustomer, LocationID: refLocation, ServiceTypeID: refService,
		Date: dt("2026-10-02"), StartTime: "09:00", LengthMinutes: 60,
	}

	t.Run("the job keeps the resolved ids and carries the records for its snapshot", func(t *testing.T) {
		repo := &memJobs{mockJobs: mockJobs{}}
		refs := &fakeRefs{}
		s := service.NewJobService(repo, service.NewOccurrenceResolver(repo), refs)
		job, err := s.CreateJob(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		if job.CustomerID != refCustomer || job.LocationID != refLocation || job.ServiceTypeID != refService || job.LengthMinutes != 60 {
			t.Fatalf("job %+v", job)
		}
		if job.Customer == nil || job.Customer.Name != "Ada" || job.Location == nil || job.ServiceType == nil {
			t.Fatalf("the job needs the loaded records: %+v", job)
		}
		if len(refs.calls) != 1 || refs.calls[0] != [3]string{refCustomer, refLocation, refService} {
			t.Fatalf("resolver calls %v", refs.calls)
		}
	})

	t.Run("a reference error is returned and nothing is saved", func(t *testing.T) {
		boom := errors.New("db down")
		for name, refsErr := range map[string]error{
			"unknown customer":     apperror.NotFound("customer not found"),
			"location not theirs":  apperror.Validation("locationId does not belong to the customer"),
			"infrastructure error": boom,
		} {
			repo := &memJobs{mockJobs: mockJobs{}}
			s := service.NewJobService(repo, service.NewOccurrenceResolver(repo), &fakeRefs{err: refsErr})
			if _, err := s.CreateJob(ctx, in); !errors.Is(err, refsErr) {
				t.Errorf("%s: %v", name, err)
			}
			if len(repo.created) != 0 {
				t.Errorf("%s: the job was saved", name)
			}
		}
	})

	t.Run("an invalid status is refused before any lookup", func(t *testing.T) {
		repo := &memJobs{mockJobs: mockJobs{}}
		refs := &fakeRefs{}
		s := service.NewJobService(repo, service.NewOccurrenceResolver(repo), refs)
		bad := in
		bad.Status = "rescheduled"
		if _, err := s.CreateJob(ctx, bad); statusOf(t, err) != 400 || len(refs.calls) != 0 {
			t.Fatalf("err=%v calls=%v", err, refs.calls)
		}
	})
}
