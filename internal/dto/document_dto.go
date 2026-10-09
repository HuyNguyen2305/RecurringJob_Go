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
	ID string `json:"id"`
	// Number is null for a draft invoice: an invoice is numbered when it is sent.
	Number         *string              `json:"number"`
	Type           string               `json:"type"`
	Status         string               `json:"status"`
	Customer       *CustomerResponse    `json:"customer"`
	Location       *LocationResponse    `json:"location"`
	ServiceType    *ServiceTypeResponse `json:"serviceType"`
	Notes          string               `json:"notes"`
	JobID          *string              `json:"jobId"`
	JobSnapshot    *model.JobSnapshot   `json:"jobSnapshot"`
	OccurrenceDate *string              `json:"occurrenceDate"`
	Revision       int                  `json:"revision"`
	SentAt         *time.Time           `json:"sentAt"`
	PaidAt         *time.Time           `json:"paidAt"`
	RefundedAt     *time.Time           `json:"refundedAt"`
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
		ID: d.ID, Number: numberOrNil(d.Number), Type: d.Type, Status: d.Status, Notes: d.Notes, Revision: d.Revision,
		SentAt: utcTime(d.SentAt), PaidAt: utcTime(d.PaidAt), RefundedAt: utcTime(d.RefundedAt),
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

// numberOrNil is the document number, or nil when it has none yet.
func numberOrNil(n string) *string {
	if n == "" {
		return nil
	}
	return &n
}

func utcTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// RevisionResponse is what an estimate said before an edit replaced it.
type RevisionResponse struct {
	Revision      int                `json:"revision"`
	Notes         string             `json:"notes"`
	CustomerID    string             `json:"customerId"`
	LocationID    string             `json:"locationId"`
	ServiceTypeID string             `json:"serviceTypeId"`
	LineItems     []LineItemResponse `json:"lineItems"`
	TotalCents    int64              `json:"totalCents"`
	ReplacedAt    time.Time          `json:"replacedAt"`
}

// NewRevisionResponses converts a list (never nil).
func NewRevisionResponses(in []model.DocumentRevision) []RevisionResponse {
	out := make([]RevisionResponse, 0, len(in))
	for _, r := range in {
		items := make([]LineItemResponse, 0, len(r.Content.LineItems))
		for _, l := range r.Content.LineItems {
			items = append(items, LineItemResponse{
				Description: l.Description, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents,
				TotalCents: int64(l.Quantity) * l.UnitPriceCents,
			})
		}
		out = append(out, RevisionResponse{
			Revision: r.Revision, Notes: r.Content.Notes, CustomerID: r.Content.CustomerID, LocationID: r.Content.LocationID,
			ServiceTypeID: r.Content.ServiceTypeID, LineItems: items, TotalCents: r.Content.TotalCents(), ReplacedAt: r.CreatedAt.UTC(),
		})
	}
	return out
}
