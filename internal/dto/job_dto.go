package dto

import (
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

// CreateJobRequest is the POST /jobs body.
type CreateJobRequest struct {
	CustomerID    string           `json:"customerId" binding:"required"`
	LocationID    string           `json:"locationId" binding:"required"`
	ServiceTypeID string           `json:"serviceTypeId" binding:"required"`
	Date          string           `json:"date" binding:"required" example:"2026-10-02"`
	StartTime     string           `json:"startTime" binding:"required" example:"09:00"`
	LengthMinutes int              `json:"lengthMinutes" binding:"required" example:"60"`
	Status        string           `json:"status" example:"unconfirmed"`
	Recurrence    *recurrence.Rule `json:"recurrence"`
}

// JobResponse is a job as returned by the API.
type JobResponse struct {
	ID            string           `json:"id"`
	CustomerID    string           `json:"customerId"`
	LocationID    string           `json:"locationId"`
	ServiceTypeID string           `json:"serviceTypeId"`
	Date          string           `json:"date"`
	StartTime     string           `json:"startTime"`
	LengthMinutes int              `json:"lengthMinutes"`
	Status        string           `json:"status"`
	Recurrence    *recurrence.Rule `json:"recurrence"`
}

func NewJobResponse(j *model.Job) JobResponse {
	return JobResponse{
		ID: j.ID, CustomerID: j.CustomerID, LocationID: j.LocationID, ServiceTypeID: j.ServiceTypeID,
		Date: civil.Format(j.Date), StartTime: j.StartTime, LengthMinutes: j.LengthMinutes,
		Status: j.Status, Recurrence: j.Recurrence,
	}
}
