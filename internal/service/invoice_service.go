package service

import (
	"context"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

// OccurrenceAvailability is the occurrence check an invoice needs before it
// attaches to an occurrence. OccurrenceService.AvailableFor satisfies it.
type OccurrenceAvailability interface {
	AvailableFor(ctx context.Context, jobID string, date time.Time) error
}

// InvoiceStore is the storage the invoice service needs: the shared document
// storage plus the occurrence and job lookups behind voiding and paid checks.
type InvoiceStore interface {
	DocumentStore
	// UpdateStatusForOccurrence moves the occurrence's invoices that are in
	// one of allowedFrom to status and returns how many changed.
	UpdateStatusForOccurrence(ctx context.Context, jobID string, date time.Time, allowedFrom []string, status string) (int64, error)
	// IDsForOccurrence returns the ids of the occurrence's invoices in status.
	IDsForOccurrence(ctx context.Context, jobID string, date time.Time, status string) ([]string, error)
	// ExistsForJob reports whether the job has an invoice in status.
	ExistsForJob(ctx context.Context, jobID, status string) (bool, error)
}

// InvoiceService holds the invoice business rules. Get, List, Update and
// ChangeStatus come from the shared documentCore.
type InvoiceService struct {
	*documentCore
	invoices InvoiceStore
	jobs     JobGetter
	occs     OccurrenceAvailability
}

func NewInvoiceService(invoices InvoiceStore, jobs JobGetter, occs OccurrenceAvailability) *InvoiceService {
	return &InvoiceService{
		documentCore: &documentCore{docType: model.DocTypeInvoice, store: invoices},
		invoices:     invoices,
		jobs:         jobs,
		occs:         occs,
	}
}

// VoidUnpaidForOccurrence voids the occurrence's draft and sent invoices, for
// when the occurrence is canceled or terminated, and returns how many it
// voided. A paid invoice is left alone: the money was received. Call it
// inside the occurrence's transaction so both changes commit together.
func (s *InvoiceService) VoidUnpaidForOccurrence(ctx context.Context, jobID string, date time.Time) (int64, error) {
	return s.invoices.UpdateStatusForOccurrence(ctx, jobID, civil.Truncate(date), []string{DocStatusDraft, DocStatusSent}, DocStatusVoid)
}

// PaidIDsForOccurrence returns the ids of the occurrence's paid invoices.
func (s *InvoiceService) PaidIDsForOccurrence(ctx context.Context, jobID string, date time.Time) ([]string, error) {
	return s.invoices.IDsForOccurrence(ctx, jobID, civil.Truncate(date), DocStatusPaid)
}

// HasPaidForJob reports whether any invoice of the job is paid.
func (s *InvoiceService) HasPaidForJob(ctx context.Context, jobID string) (bool, error) {
	return s.invoices.ExistsForJob(ctx, jobID, DocStatusPaid)
}

// Create saves a draft invoice for one occurrence of a job. The occurrence
// must exist and be available (404 / 409 otherwise); a second live invoice for
// the same occurrence is a 409 (a voided one does not count). The job is
// snapshotted onto the invoice.
func (s *InvoiceService) Create(ctx context.Context, jobID string, date time.Time, in DocumentInput) (*model.CustomerDocument, error) {
	if err := ValidateID("job id", jobID); err != nil {
		return nil, err
	}
	doc, err := buildDocument(in)
	if err != nil {
		return nil, err
	}
	date = civil.Truncate(date)
	if err := s.occs.AvailableFor(ctx, jobID, date); err != nil {
		return nil, err
	}
	job, err := s.jobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	// An invoice is for the job's customer, location and service type.
	doc.CustomerID, doc.LocationID, doc.ServiceTypeID = job.CustomerID, job.LocationID, job.ServiceTypeID
	doc.Customer, doc.Location, doc.ServiceType = job.Customer, job.Location, job.ServiceType
	doc.JobID = &job.ID
	doc.JobSnapshot = snapshotOf(job)
	doc.OccurrenceDate = &date
	if err := s.invoices.Create(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// Update edits a draft invoice's notes and lines. Its customer, location and
// service type cannot be changed: they come from the job.
func (s *InvoiceService) Update(ctx context.Context, id string, p DocumentPatch) (*model.CustomerDocument, error) {
	if p.hasRefs() {
		return nil, apperror.Validation("an invoice's customer, location and service type come from its job and cannot be changed")
	}
	return s.update(ctx, id, p, nil)
}
