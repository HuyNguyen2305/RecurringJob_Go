package handler

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
)

// SettingsService is what the settings handler needs from the service layer.
type SettingsService interface {
	Get(ctx context.Context) (*model.TenantSettings, error)
	UpdateTimezone(ctx context.Context, tz string) (*model.TenantSettings, error)
	TodayFor(tz string) time.Time
}

type SettingsHandler struct {
	settings SettingsService
}

func NewSettingsHandler(settings SettingsService) *SettingsHandler {
	return &SettingsHandler{settings: settings}
}

// Get godoc
// @Summary  Get the tenant settings (time zone and today's date in it)
// @Tags     settings
// @Produce  json
// @Success  200 {object} map[string]any
// @Router   /settings [get]
func (h *SettingsHandler) Get(c *gin.Context) {
	s, err := h.settings.Get(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "settings", dto.NewSettingsResponse(s, h.settings.TodayFor(s.Timezone)))
}

// Update godoc
// @Summary  Set the tenant time zone
// @Tags     settings
// @Accept   json
// @Produce  json
// @Param    body body dto.UpdateSettingsRequest true "settings"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Router   /settings [put]
func (h *SettingsHandler) Update(c *gin.Context) {
	var req dto.UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	s, err := h.settings.UpdateTimezone(c.Request.Context(), req.Timezone)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "settings updated", dto.NewSettingsResponse(s, h.settings.TodayFor(s.Timezone)))
}
