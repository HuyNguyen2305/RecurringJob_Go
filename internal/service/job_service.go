package service

import (
	"context"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

const (
	DefaultOccurrencesLimit = 100
	MaxLengthMinutes        = 1440 // one day
)

// JobRepository is the storage the job service needs.
type JobRepository interface {
	JobGetter
	Create(ctx context.Context, job *model.Job) error
}

// CreateJobInput is a validated-shape request to create a job. StartTime is
// "HH:MM" or "HH:MM:SS".
type CreateJobInput struct {
	CustomerID    string
	LocationID    string
	ServiceTypeID string
	Date          time.Time
	StartTime     string
	LengthMinutes int
	Status        string
	Recurrence    *recurrence.Rule
}

type JobService struct {
	jobs     JobRepository
	resolver *OccurrenceResolver
	refs     ReferenceResolver
}

func NewJobService(jobs JobRepository, resolver *OccurrenceResolver, refs ReferenceResolver) *JobService {
	return &JobService{jobs: jobs, resolver: resolver, refs: refs}
}

// parseStartTime accepts "HH:MM" or "HH:MM:SS" and returns "HH:MM:SS".
func parseStartTime(s string) (string, error) {
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("15:04:05"), nil
		}
	}
	return "", apperror.Validation("startTime must be HH:MM")
}

// CreateJob validates the status, references, start time and recurrence and
// saves the job.
func (s *JobService) CreateJob(ctx context.Context, in CreateJobInput) (*model.Job, error) {
	if in.Status == "" {
		in.Status = StatusUnconfirmed
	}
	if err := ValidateJobCreateStatus(in.Status); err != nil {
		return nil, err
	}
	start, err := parseStartTime(in.StartTime)
	if err != nil {
		return nil, err
	}
	if in.LengthMinutes < 1 || in.LengthMinutes > MaxLengthMinutes {
		return nil, bad("lengthMinutes must be between 1 and %d", MaxLengthMinutes)
	}
	refs, err := s.refs.Resolve(ctx, in.CustomerID, in.LocationID, in.ServiceTypeID)
	if err != nil {
		return nil, err
	}
	date := civil.Truncate(in.Date)
	if err := ValidateRecurrence(ctx, in.Recurrence, date, s.jobs); err != nil {
		return nil, err
	}
	job := &model.Job{
		CustomerID: refs.Customer.ID, LocationID: refs.Location.ID, ServiceTypeID: refs.ServiceType.ID,
		Date: date, StartTime: start, LengthMinutes: in.LengthMinutes, Status: in.Status, Recurrence: in.Recurrence,
		Customer: refs.Customer, Location: refs.Location, ServiceType: refs.ServiceType,
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

// Occurrences returns the job's plain occurrence dates. limit 0 means the
// default; otherwise it must be 1..1000.
func (s *JobService) Occurrences(ctx context.Context, jobID string, from, to time.Time, limit int) ([]string, error) {
	if err := ValidateID("job id", jobID); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = DefaultOccurrencesLimit
	}
	if limit < 1 || limit > MaxScheduleLimit {
		return nil, apperror.Validation("limit must be between 1 and 1000")
	}
	if !from.IsZero() && !to.IsZero() && from.After(to) {
		return nil, apperror.Validation("from cannot be after to")
	}
	job, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	return s.resolver.ResolveOccurrences(ctx, job, Window{From: from, To: to, Limit: limit}, nil)
}
