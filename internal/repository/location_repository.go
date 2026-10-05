package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

type LocationRepository struct {
	BaseRepository
}

func NewLocationRepository(db *gorm.DB) *LocationRepository {
	return &LocationRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *LocationRepository) Create(ctx context.Context, l *model.Location) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Create(l).Error })
}

// Get returns the location or an apperror NotFound.
func (r *LocationRepository) Get(ctx context.Context, id string) (*model.Location, error) {
	var l model.Location
	err := r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Where("id = ?", id).First(&l).Error })
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.NotFound("location not found")
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ListByCustomer returns the customer's locations in creation order.
func (r *LocationRepository) ListByCustomer(ctx context.Context, customerID string) ([]model.Location, error) {
	out := []model.Location{}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Where("customer_id = ?", customerID).Order("created_at, id").Find(&out).Error
	})
	return out, err
}
