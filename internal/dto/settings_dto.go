package dto

import (
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

// UpdateSettingsRequest is the PUT /settings body.
type UpdateSettingsRequest struct {
	Timezone string `json:"timezone" binding:"required" example:"America/Los_Angeles"`
}

// SettingsResponse is the tenant's settings plus today's date in its zone.
type SettingsResponse struct {
	Timezone string `json:"timezone"`
	Today    string `json:"today"`
}

func NewSettingsResponse(s *model.TenantSettings, today time.Time) SettingsResponse {
	return SettingsResponse{Timezone: s.Timezone, Today: civil.Format(today)}
}
