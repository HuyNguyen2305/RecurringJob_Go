package service

import (
	"context"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

// maxTimezoneLen bounds the stored name (the longest IANA names are ~30).
const maxTimezoneLen = 64

// SettingsStore is what the settings service needs from the repository.
type SettingsStore interface {
	Get(ctx context.Context) (*model.TenantSettings, error)
	SetTimezone(ctx context.Context, tz string) (*model.TenantSettings, error)
}

// TodayProvider tells what the current calendar date is for the request's
// tenant. Services that compare dates with "today" depend on it.
type TodayProvider interface {
	Today(ctx context.Context) (time.Time, error)
}

// SettingsService manages per-tenant settings and answers "what is today".
type SettingsService struct {
	store SettingsStore
	now   func() time.Time
}

func NewSettingsService(store SettingsStore) *SettingsService {
	return &SettingsService{store: store, now: time.Now}
}

// WithClock replaces the clock (for tests).
func (s *SettingsService) WithClock(now func() time.Time) *SettingsService {
	s.now = now
	return s
}

// Get returns the tenant's settings (UTC when none were saved).
func (s *SettingsService) Get(ctx context.Context) (*model.TenantSettings, error) {
	return s.store.Get(ctx)
}

// UpdateTimezone validates and saves the tenant's IANA time zone.
func (s *SettingsService) UpdateTimezone(ctx context.Context, tz string) (*model.TenantSettings, error) {
	if tz == "" {
		return nil, apperror.Validation("timezone is required")
	}
	if len(tz) > maxTimezoneLen {
		return nil, apperror.Validation("timezone is too long")
	}
	// "Local" would mean the server's zone, which is not a tenant setting.
	if tz == "Local" {
		return nil, apperror.Validation("timezone must be an IANA name such as America/Los_Angeles")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, apperror.Validation("unknown timezone " + tz + "; use an IANA name such as America/Los_Angeles")
	}
	return s.store.SetTimezone(ctx, tz)
}

// Today returns the tenant's current calendar date as a civil date. A stored
// zone that cannot be loaded falls back to UTC instead of failing requests.
func (s *SettingsService) Today(ctx context.Context) (time.Time, error) {
	settings, err := s.store.Get(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return s.TodayFor(settings.Timezone), nil
}

// currentDate is today's civil date from the provider, or the UTC date of now
// when no provider is set.
func currentDate(ctx context.Context, p TodayProvider, now func() time.Time) (time.Time, error) {
	if p == nil {
		return civil.Truncate(now()), nil
	}
	return p.Today(ctx)
}

// TodayFor returns the current date in tz (UTC when tz does not load); the
// API shows it next to the zone.
func (s *SettingsService) TodayFor(tz string) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	n := s.now().In(loc)
	return civil.New(n.Year(), n.Month(), n.Day())
}
