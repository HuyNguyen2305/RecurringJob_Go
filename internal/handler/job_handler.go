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

// JobService is what the job handler needs from the service layer.
type JobService interface {
	CreateJob(ctx context.Context, in service.CreateJobInput) (*model.Job, error)
	Occurrences(ctx context.Context, jobID string, from, to time.Time, limit int) ([]string, error)
}

type JobHandler struct {
	jobs JobService
}

func NewJobHandler(jobs JobService) *JobHandler {
	return &JobHandler{jobs: jobs}
}

// Create godoc
// @Summary  Create a job (one-off or recurring)
// @Tags     jobs
// @Accept   json
// @Produce  json
// @Param    body body dto.CreateJobRequest true "job"
// @Success  200 {object} map[string]any
// @Failure  400 {object} map[string]any
// @Failure  404 {object} map[string]any
// @Router   /jobs [post]
func (h *JobHandler) Create(c *gin.Context) {
	var req dto.CreateJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, apperror.Validation(err.Error()))
		return
	}
	date, err := parseDate("date", req.Date)
	if err != nil {
		fail(c, err)
		return
	}
	job, err := h.jobs.CreateJob(c.Request.Context(), service.CreateJobInput{
		Date: date, Status: req.Status, Recurrence: req.Recurrence,
	})
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "job created", dto.NewJobResponse(job))
}

// Occurrences godoc
// @Summary  List a job's occurrence dates
// @Tags     jobs
// @Produce  json
// @Param    id    path  string true  "job id"
// @Param    from  query string false "YYYY-MM-DD"
// @Param    to    query string false "YYYY-MM-DD"
// @Param    limit query int    false "1..1000 (default 100)"
// @Success  200 {object} map[string]any
// @Router   /jobs/{id}/occurrences [get]
func (h *JobHandler) Occurrences(c *gin.Context) {
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
	dates, err := h.jobs.Occurrences(c.Request.Context(), c.Param("id"), from, to, limit)
	if err != nil {
		fail(c, err)
		return
	}
	ok(c, "occurrences", dates)
}
