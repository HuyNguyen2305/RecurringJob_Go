package dto

import (
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

// TaskRequest is one task of a work order.
type TaskRequest struct {
	Description string `json:"description" binding:"required" example:"Clean the windows"`
	Done        bool   `json:"done"`
}

// CreateWorkOrderRequest is the body that creates a work order for one
// occurrence. The customer, location and service type come from the job.
type CreateWorkOrderRequest struct {
	Notes string        `json:"notes"`
	Tasks []TaskRequest `json:"tasks" binding:"dive"`
}

// UpdateWorkOrderRequest edits a work order; omitted fields stay unchanged and
// a given tasks list replaces all tasks.
type UpdateWorkOrderRequest struct {
	Notes *string        `json:"notes"`
	Tasks *[]TaskRequest `json:"tasks" binding:"omitempty,dive"`
}

// WorkOrderStatusRequest is the body of the status endpoint.
type WorkOrderStatusRequest struct {
	Status string `json:"status" binding:"required" example:"scheduled"`
}

// TaskResponse is a task as returned by the API.
type TaskResponse struct {
	Description string `json:"description"`
	Done        bool   `json:"done"`
}

// WorkOrderResponse is a work order as returned by the API.
type WorkOrderResponse struct {
	ID             string               `json:"id"`
	Number         string               `json:"number"`
	Status         string               `json:"status"`
	Customer       *CustomerResponse    `json:"customer"`
	Location       *LocationResponse    `json:"location"`
	ServiceType    *ServiceTypeResponse `json:"serviceType"`
	Notes          string               `json:"notes"`
	JobID          string               `json:"jobId"`
	JobSnapshot    *model.JobSnapshot   `json:"jobSnapshot"`
	OccurrenceDate string               `json:"occurrenceDate"`
	Tasks          []TaskResponse       `json:"tasks"`
	CompletedAt    *time.Time           `json:"completedAt"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
}

func NewWorkOrderResponse(w *model.WorkOrder) WorkOrderResponse {
	tasks := make([]TaskResponse, 0, len(w.Tasks))
	for _, t := range w.Tasks {
		tasks = append(tasks, TaskResponse{Description: t.Description, Done: t.Done})
	}
	out := WorkOrderResponse{
		ID: w.ID, Number: w.Number, Status: w.Status, Notes: w.Notes, JobID: w.JobID, JobSnapshot: w.JobSnapshot,
		OccurrenceDate: civil.Format(w.OccurrenceDate), Tasks: tasks, CompletedAt: utcTime(w.CompletedAt),
		CreatedAt: w.CreatedAt.UTC(), UpdatedAt: w.UpdatedAt.UTC(),
	}
	if w.Customer != nil {
		c := NewCustomerResponse(w.Customer)
		out.Customer = &c
	}
	if w.Location != nil {
		l := NewLocationResponse(w.Location)
		out.Location = &l
	}
	if w.ServiceType != nil {
		s := NewServiceTypeResponse(w.ServiceType)
		out.ServiceType = &s
	}
	return out
}

// NewWorkOrderResponses converts a list (never nil).
func NewWorkOrderResponses(in []model.WorkOrder) []WorkOrderResponse {
	out := make([]WorkOrderResponse, 0, len(in))
	for i := range in {
		out = append(out, NewWorkOrderResponse(&in[i]))
	}
	return out
}
