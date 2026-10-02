package handler

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/dto"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// OccurrenceService is what the occurrence handler needs from the service layer.
type OccurrenceService interface {
	Schedule(ctx context.Context, jobID string, q service.ScheduleQuery) ([]service.ScheduleItem, error)
	UpdateOccurrence(ctx context.Context, jobID string, date time.Time, req service.UpdateOccurrenceRequest) (*model.JobOccurrence, error)
}

type OccurrenceHandler struct {
	occs OccurrenceService
}

func NewOccurrenceHandler(occs OccurrenceService) *OccurrenceHandler {
	return &OccurrenceHandler{occs: occs}
}

// Schedule godoc
// @Summary  A job's schedule with real/hollow/overdue state
// @Tags     occurrences
// @Produce  json
// @Param    id    path  string true  "job id"
// @Param    from  query string false "YYYY-MM-DD"
// @Param    to    query string false "YYYY-MM-DD"
// @Param    limit query int    false "1..1000 (default 100)"
// @Success  200 {object} map[string]any
// @Router   /jobs/{id}/schedule [get]
func (h *OccurrenceHandler) Schedule(c *gin.Context) {
	from, err := optionalDate(c, "from")
	if err != nil {
		fail(c, err)
		return
	}
	to, err := optionalDate(c, "to")
	if err != nil {
		fail(c, err)
		return
	}
	limit, err := optionalInt(c, "limit")
	if err != nil {
		fail(c, err)
		return
	}
	items, err := h.occs.Schedule(c.Request.Context(), c.Param("id"), service.ScheduleQuery{From: from, To: to, Limit: limit})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "schedule", items)
}

// Update godoc
// @Summary  Change the status of one occurrence
// @Tags     occurrences
// @Accept   json
// @Produce  json
// @Param    id   path string true "job id"
// @Param    date path string true "occurrence date, YYYY-MM-DD"
// @Param    body body dto.UpdateOccurrenceRequest true "new status"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Failure  409 {object} map[string]any
// @Router   /jobs/{id}/occurrences/{date} [patch]
func (h *OccurrenceHandler) Update(c *gin.Context) {
	date, err := parseDate("date", c.Param("date"))
	if err != nil {
		fail(c, err)
		return
	}
	var req dto.UpdateOccurrenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	in := service.UpdateOccurrenceRequest{Status: req.Status}
	if req.RescheduledTo != nil {
		to, err := parseDate("rescheduledTo", *req.RescheduledTo)
		if err != nil {
			fail(c, err)
			return
		}
		in.RescheduledTo = &to
	}
	saved, err := h.occs.UpdateOccurrence(c.Request.Context(), c.Param("id"), date, in)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "occurrence updated", dto.NewOccurrenceResponse(saved))
}
