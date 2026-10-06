package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"recurringjob/internal/model"
)

// DefaultTimezone is used when a tenant has no settings row yet.
const DefaultTimezone = "UTC"

type SettingsRepository struct {
	BaseRepository
}

func NewSettingsRepository(db *gorm.DB) *SettingsRepository {
	return &SettingsRepository{BaseRepository: NewBaseRepository(db)}
}

// Get returns the tenant's settings, or the defaults when no row exists.
func (r *SettingsRepository) Get(ctx context.Context) (*model.TenantSettings, error) {
	var s model.TenantSettings
	err := r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Where("id = 1").First(&s).Error })
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.TenantSettings{ID: 1, Timezone: DefaultTimezone}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// SetTimezone stores the time zone, creating the row when it is missing, and
// returns the saved settings.
func (r *SettingsRepository) SetTimezone(ctx context.Context, tz string) (*model.TenantSettings, error) {
	s := model.TenantSettings{ID: 1, Timezone: tz}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			DoUpdates: clause.Assignments(map[string]any{"timezone": tz, "updated_at": gorm.Expr("now()")}),
		}).Create(&s).Error
	})
	if err != nil {
		return nil, err
	}
	return r.Get(ctx)
}
