package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

type ServiceTypeRepository struct {
	BaseRepository
}

func NewServiceTypeRepository(db *gorm.DB) *ServiceTypeRepository {
	return &ServiceTypeRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *ServiceTypeRepository) Create(ctx context.Context, s *model.ServiceType) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Create(s).Error })
}

// Get returns the service type or an apperror NotFound.
func (r *ServiceTypeRepository) Get(ctx context.Context, id string) (*model.ServiceType, error) {
	var s model.ServiceType
	err := r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Where("id = ?", id).First(&s).Error })
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.NotFound("service type not found")
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// List returns service types by name, then id.
func (r *ServiceTypeRepository) List(ctx context.Context) ([]model.ServiceType, error) {
	out := []model.ServiceType{}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Order("name, id").Find(&out).Error })
	return out, err
}
