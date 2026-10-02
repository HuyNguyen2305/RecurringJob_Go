package service

import (
	"context"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

const DefaultOccurrencesLimit = 100

// JobRepository is the storage the job service needs.
type JobRepository interface {
	JobGetter
	Create(ctx context.Context, job *model.Job) error
}

// CreateJobInput is a validated-shape request to create a job.
type CreateJobInput struct {
	Date       time.Time
	Status     string
	Recurrence *recurrence.Rule
}

type JobService struct {
	jobs     JobRepository
	resolver *OccurrenceResolver
}

func NewJobService(jobs JobRepository, resolver *OccurrenceResolver) *JobService {
	return &JobService{jobs: jobs, resolver: resolver}
}

// CreateJob validates the status and recurrence and saves the job.
func (s *JobService) CreateJob(ctx context.Context, in CreateJobInput) (*model.Job, error) {
	if in.Status == "" {
		in.Status = StatusUnconfirmed
	}
	if err := ValidateJobCreateStatus(in.Status); err != nil {
		return nil, err
	}
	date := civil.Truncate(in.Date)
	if err := ValidateRecurrence(ctx, in.Recurrence, date, s.jobs); err != nil {
		return nil, err
	}
	job := &model.Job{Date: date, Status: in.Status, Recurrence: in.Recurrence}
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
