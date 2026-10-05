package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

type CustomerRepository struct {
	BaseRepository
}

func NewCustomerRepository(db *gorm.DB) *CustomerRepository {
	return &CustomerRepository{BaseRepository: NewBaseRepository(db)}
}

func (r *CustomerRepository) Create(ctx context.Context, c *model.Customer) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Create(c).Error })
}

// Get returns the customer or an apperror NotFound.
func (r *CustomerRepository) Get(ctx context.Context, id string) (*model.Customer, error) {
	var c model.Customer
	err := r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Where("id = ?", id).First(&c).Error })
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.NotFound("customer not found")
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// List returns customers by name, then id.
func (r *CustomerRepository) List(ctx context.Context, limit, offset int) ([]model.Customer, error) {
	out := []model.Customer{}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Order("name, id").Limit(limit).Offset(offset).Find(&out).Error
	})
	return out, err
}

// Update applies fields and returns the rows affected (0 = unknown id).
func (r *CustomerRepository) Update(ctx context.Context, id string, fields map[string]any) (int64, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Model(&model.Customer{}).Where("id = ?", id).Updates(fields)
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}
