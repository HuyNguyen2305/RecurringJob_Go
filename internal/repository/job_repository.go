package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

type JobRepository struct {
	BaseRepository
}

func NewJobRepository(db *gorm.DB) *JobRepository {
	return &JobRepository{BaseRepository: NewBaseRepository(db)}
}

// GetJob returns the job or an apperror NotFound.
func (r *JobRepository) GetJob(ctx context.Context, id string) (*model.Job, error) {
	var job model.Job
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Where("id = ?", id).First(&job).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.NotFound("job not found")
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *JobRepository) Create(ctx context.Context, job *model.Job) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Create(job).Error
	})
}
