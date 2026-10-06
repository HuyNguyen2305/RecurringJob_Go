package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

type JobRepository struct {
	BaseRepository
}

func NewJobRepository(db *gorm.DB) *JobRepository {
	return &JobRepository{BaseRepository: NewBaseRepository(db)}
}

// GetJob returns the job with its customer, location and service type, or an
// apperror NotFound.
func (r *JobRepository) GetJob(ctx context.Context, id string) (*model.Job, error) {
	var job model.Job
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Preload("Customer").Preload("Location").Preload("ServiceType").
			Where("id = ?", id).First(&job).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.NotFound("job not found")
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// LockJob locks the job row until the surrounding transaction ends, so
// anything that needs the job (an invoice or an occurrence row pointing at
// it) waits. It is a NotFound when the job does not exist. Call it inside
// Transaction.
func (r *JobRepository) LockJob(ctx context.Context, id string) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error {
		var locked model.Job
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", id).First(&locked).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.NotFound("job not found")
		}
		return err
	})
}

// Delete removes the job. It is a 409 while a document or an
// invoice still points at it.
func (r *JobRepository) Delete(ctx context.Context, id string) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error {
		err := tx.Where("id = ?", id).Delete(&model.Job{}).Error
		if isForeignKeyViolationOn(err, "job_id") {
			return apperror.Conflict("the job is still in use")
		}
		return err
	})
}

// Create saves the job. Its customer, location and service type are read-only
// and are not written.
func (r *JobRepository) Create(ctx context.Context, job *model.Job) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Omit("Customer", "Location", "ServiceType").Create(job).Error
	})
}
