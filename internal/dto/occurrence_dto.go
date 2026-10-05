package dto

import (
	"time"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

// UpdateOccurrenceRequest is the PATCH /jobs/{id}/occurrences/{date} body.
type UpdateOccurrenceRequest struct {
	Status        string  `json:"status" binding:"required" example:"rescheduled"`
	RescheduledTo *string `json:"rescheduledTo" example:"2026-10-12"`
}

// OccurrenceResponse is a stored occurrence row.
type OccurrenceResponse struct {
	JobID           string     `json:"jobId"`
	Date            string     `json:"date"`
	Status          string     `json:"status"`
	RescheduledTo   *string    `json:"rescheduledTo"`
	RescheduledFrom *string    `json:"rescheduledFrom"`
	CompletedAt     *time.Time `json:"completedAt"`
	// PaidInvoiceIDs appears only when a canceled or terminated occurrence has
	// paid invoices that were kept and need follow-up.
	PaidInvoiceIDs []string `json:"paidInvoiceIds,omitempty"`
}

func NewOccurrenceResponse(o *model.JobOccurrence) OccurrenceResponse {
	var completed *time.Time
	if o.CompletedAt != nil {
		utc := o.CompletedAt.UTC()
		completed = &utc
	}
	return OccurrenceResponse{
		JobID:           o.JobID,
		Date:            civil.Format(o.OccurrenceDate),
		Status:          o.Status,
		RescheduledTo:   fmtDate(o.RescheduledTo),
		RescheduledFrom: fmtDate(o.RescheduledFrom),
		CompletedAt:     completed,
		PaidInvoiceIDs:  o.PaidInvoiceIDs,
	}
}

func fmtDate(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := civil.Format(*t)
	return &s
}
