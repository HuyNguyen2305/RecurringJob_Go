package dto

import (
	"time"

	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

// LineItemRequest is one line of an estimate or invoice. Money is in cents.
type LineItemRequest struct {
	Description    string `json:"description" binding:"required" example:"Window cleaning"`
	Quantity       int    `json:"quantity" binding:"required" example:"2"`
	UnitPriceCents int64  `json:"unitPriceCents" example:"7500"`
}

// CreateEstimateRequest is the POST /estimates body: who it is for and what it
// quotes.
type CreateEstimateRequest struct {
	CustomerID    string            `json:"customerId" binding:"required"`
	LocationID    string            `json:"locationId" binding:"required"`
	ServiceTypeID string            `json:"serviceTypeId" binding:"required"`
	Notes         string            `json:"notes"`
	LineItems     []LineItemRequest `json:"lineItems" binding:"dive"`
}

// CreateInvoiceRequest is the body that creates an invoice for one occurrence.
// The customer, location and service type come from the job.
type CreateInvoiceRequest struct {
	Notes     string            `json:"notes"`
	LineItems []LineItemRequest `json:"lineItems" binding:"dive"`
}

// UpdateDocumentRequest edits a document; omitted fields stay unchanged and a
// given lineItems list replaces all line items. The reference ids are only for
// an estimate that has no job yet.
type UpdateDocumentRequest struct {
	Notes         *string            `json:"notes"`
	LineItems     *[]LineItemRequest `json:"lineItems" binding:"omitempty,dive"`
	CustomerID    *string            `json:"customerId"`
	LocationID    *string            `json:"locationId"`
	ServiceTypeID *string            `json:"serviceTypeId"`
}

// DocumentStatusRequest is the body of the status endpoints.
type DocumentStatusRequest struct {
	Status string `json:"status" binding:"required" example:"sent"`
}

// ApproveEstimateRequest describes the job an approved estimate becomes. The
// job's customer, location and service type come from the estimate.
type ApproveEstimateRequest struct {
	Date          string           `json:"date" binding:"required" example:"2026-10-12"`
	StartTime     string           `json:"startTime" binding:"required" example:"09:00"`
	LengthMinutes int              `json:"lengthMinutes" binding:"required" example:"60"`
	Recurrence    *recurrence.Rule `json:"recurrence"`
}

// LineItemResponse is a line item with its computed total.
type LineItemResponse struct {
	Description    string `json:"description"`
	Quantity       int    `json:"quantity"`
	UnitPriceCents int64  `json:"unitPriceCents"`
	TotalCents     int64  `json:"totalCents"`
}

// DocumentResponse is an estimate or invoice as returned by the API.
type DocumentResponse struct {
	ID             string               `json:"id"`
	Type           string               `json:"type"`
	Status         string               `json:"status"`
	Customer       *CustomerResponse    `json:"customer"`
	Location       *LocationResponse    `json:"location"`
	ServiceType    *ServiceTypeResponse `json:"serviceType"`
	Notes          string               `json:"notes"`
	JobID          *string              `json:"jobId"`
	JobSnapshot    *model.JobSnapshot   `json:"jobSnapshot"`
	OccurrenceDate *string              `json:"occurrenceDate"`
	LineItems      []LineItemResponse   `json:"lineItems"`
	SubtotalCents  int64                `json:"subtotalCents"`
	TotalCents     int64                `json:"totalCents"`
	CreatedAt      time.Time            `json:"createdAt"`
	UpdatedAt      time.Time            `json:"updatedAt"`
}

func NewDocumentResponse(d *model.CustomerDocument) DocumentResponse {
	items := make([]LineItemResponse, 0, len(d.LineItems))
	for _, it := range d.LineItems {
		items = append(items, LineItemResponse{
			Description: it.Description, Quantity: it.Quantity,
			UnitPriceCents: it.UnitPriceCents, TotalCents: it.TotalCents(),
		})
	}
	out := DocumentResponse{
		ID: d.ID, Type: d.Type, Status: d.Status, Notes: d.Notes,
		JobID: d.JobID, JobSnapshot: d.JobSnapshot, OccurrenceDate: fmtDate(d.OccurrenceDate),
		LineItems: items, SubtotalCents: d.TotalCents(), TotalCents: d.TotalCents(),
		CreatedAt: d.CreatedAt.UTC(), UpdatedAt: d.UpdatedAt.UTC(),
	}
	if d.Customer != nil {
		c := NewCustomerResponse(d.Customer)
		out.Customer = &c
	}
	if d.Location != nil {
		l := NewLocationResponse(d.Location)
		out.Location = &l
	}
	if d.ServiceType != nil {
		s := NewServiceTypeResponse(d.ServiceType)
		out.ServiceType = &s
	}
	return out
}

// NewDocumentResponses converts a list.
func NewDocumentResponses(docs []model.CustomerDocument) []DocumentResponse {
	out := make([]DocumentResponse, 0, len(docs))
	for i := range docs {
		out = append(out, NewDocumentResponse(&docs[i]))
	}
	return out
}
