// Package dto holds request/response shapes for the HTTP API.
package dto

import (
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

// CreateJobRequest is the POST /jobs body.
type CreateJobRequest struct {
	Date       string           `json:"date" binding:"required" example:"2026-10-02"`
	Status     string           `json:"status" example:"unconfirmed"`
	Recurrence *recurrence.Rule `json:"recurrence"`
}

// JobResponse is a job as returned by the API.
type JobResponse struct {
	ID         string           `json:"id"`
	Date       string           `json:"date"`
	Status     string           `json:"status"`
	Recurrence *recurrence.Rule `json:"recurrence"`
}

func NewJobResponse(j *model.Job) JobResponse {
	return JobResponse{ID: j.ID, Date: civil.Format(j.Date), Status: j.Status, Recurrence: j.Recurrence}
}
