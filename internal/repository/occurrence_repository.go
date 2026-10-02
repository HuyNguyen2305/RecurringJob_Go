package repository

import (
	"context"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

type OccurrenceRepository struct {
	BaseRepository
}

func NewOccurrenceRepository(db *gorm.DB) *OccurrenceRepository {
	return &OccurrenceRepository{BaseRepository: NewBaseRepository(db)}
}

// ListByJob returns every stored occurrence row of the job.
func (r *OccurrenceRepository) ListByJob(ctx context.Context, jobID string) ([]model.JobOccurrence, error) {
	var rows []model.JobOccurrence
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Where("job_id = ?", jobID).Order("occurrence_date").Find(&rows).Error
	})
	return rows, err
}

// Create inserts a row; a unique (job_id, occurrence_date) violation is a 409.
func (r *OccurrenceRepository) Create(ctx context.Context, occ *model.JobOccurrence) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error {
		err := tx.Create(occ).Error
		if isUniqueViolation(err) {
			return apperror.Conflict("occurrence was changed by another request")
		}
		return err
	})
}

// UpdateGuarded applies updates only while the row's status is still one of
// allowedFrom (optimistic guard) and returns the rows affected; 0 means the
// row changed under the caller.
func (r *OccurrenceRepository) UpdateGuarded(ctx context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Model(&model.JobOccurrence{}).
			Where("id = ? AND status IN ?", id, allowedFrom).
			Updates(updates)
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}
