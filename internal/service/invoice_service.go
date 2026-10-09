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
	// CountForJob returns how many invoices, in any status, the job has.
	CountForJob(ctx context.Context, jobID string) (int64, error)
	// MoveUnpaidForOccurrence moves the draft and sent invoices of the
	// occurrence on from to the occurrence on to and returns how many moved.
	MoveUnpaidForOccurrence(ctx context.Context, jobID string, from, to time.Time) (int64, error)
	// LockOccurrence takes the transaction-scoped lock of one occurrence
	// (see OccurrenceRepository); call it inside Transaction.
	LockOccurrence(ctx context.Context, jobID string, date time.Time) error
}

// EstimateLines gives the line items of a job's estimate, which a new invoice
// starts from. EstimateRepository satisfies it.
type EstimateLines interface {
	LineItemsForJob(ctx context.Context, jobID string) ([]model.CustomerLineItem, error)
}

// InvoiceService holds the invoice business rules. Get, List, Update and
// ChangeStatus come from the shared documentCore.
type InvoiceService struct {
	*documentCore
	invoices  InvoiceStore
	jobs      JobGetter
	occs      OccurrenceAvailability
	estimates EstimateLines
}

func NewInvoiceService(invoices InvoiceStore, jobs JobGetter, occs OccurrenceAvailability) *InvoiceService {
	return &InvoiceService{
		documentCore: &documentCore{docType: model.DocTypeInvoice, store: invoices},
		invoices:     invoices,
		jobs:         jobs,
		occs:         occs,
	}
}

// WithEstimates makes an invoice created without line items start from the
// lines of its job's estimate. Without it such an invoice starts empty.
func (s *InvoiceService) WithEstimates(estimates EstimateLines) *InvoiceService {
	s.estimates = estimates
	return s
}

// VoidUnpaidForOccurrence voids the occurrence's draft and sent invoices, for
// when the occurrence is canceled or terminated, and returns how many it
// voided. A paid invoice is left alone: the money was received. Call it
// inside the occurrence's transaction so both changes commit together.
func (s *InvoiceService) VoidUnpaidForOccurrence(ctx context.Context, jobID string, date time.Time) (int64, error) {
	return s.invoices.UpdateStatusForOccurrence(ctx, jobID, civil.Truncate(date), []string{DocStatusDraft, DocStatusSent}, DocStatusVoid)
}

// MoveUnpaidForOccurrence moves the draft and sent invoices of an occurrence
// to its new date, for when the occurrence is rescheduled, and returns how
// many it moved. A paid invoice stays where it is. Call it inside the
// occurrence's transaction.
func (s *InvoiceService) MoveUnpaidForOccurrence(ctx context.Context, jobID string, from, to time.Time) (int64, error) {
	return s.invoices.MoveUnpaidForOccurrence(ctx, jobID, civil.Truncate(from), civil.Truncate(to))
}

// PaidIDsForOccurrence returns the ids of the occurrence's paid invoices.
func (s *InvoiceService) PaidIDsForOccurrence(ctx context.Context, jobID string, date time.Time) ([]string, error) {
	return s.invoices.IDsForOccurrence(ctx, jobID, civil.Truncate(date), DocStatusPaid)
}

// HasPaidForJob reports whether any invoice of the job was ever paid: it is
// paid now, or it was paid and then refunded. A refund does not undo the fact
// that the job was billed and paid.
func (s *InvoiceService) HasPaidForJob(ctx context.Context, jobID string) (bool, error) {
	for _, status := range []string{DocStatusPaid, DocStatusRefunded} {
		found, err := s.invoices.ExistsForJob(ctx, jobID, status)
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// Create saves a draft invoice for one occurrence of a job. The occurrence
// must exist and be available (404 / 409 otherwise); a second live invoice for
// the same occurrence is a 409 (a voided one does not count). The job is
// snapshotted onto the invoice. When the input has no line items at all (nil,
// as opposed to an explicit empty list) the invoice starts with the lines of
// the job's estimate, if it has one.
func (s *InvoiceService) Create(ctx context.Context, jobID string, date time.Time, in DocumentInput) (*model.CustomerDocument, error) {
	if err := ValidateID("job id", jobID); err != nil {
		return nil, err
	}
	doc, err := buildDocument(in)
	if err != nil {
		return nil, err
	}
	date = civil.Truncate(date)
	// The occurrence lock makes the availability check and the insert one
	// step with respect to a status change of the same occurrence: a cancel
	// cannot slip in between and leave a live invoice on a canceled visit.
	err = s.invoices.Transaction(ctx, func(ctx context.Context) error {
		if err := s.invoices.LockOccurrence(ctx, jobID, date); err != nil {
			return err
		}
		if err := s.occs.AvailableFor(ctx, jobID, date); err != nil {
			return err
		}
		job, err := s.jobs.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		// An invoice is for the job's customer, location and service type.
		doc.CustomerID, doc.LocationID, doc.ServiceTypeID = job.CustomerID, job.LocationID, job.ServiceTypeID
		doc.Customer, doc.Location, doc.ServiceType = job.Customer, job.Location, job.ServiceType
		doc.JobID = &job.ID
		doc.JobSnapshot = snapshotOf(job)
		doc.OccurrenceDate = &date
		if in.LineItems == nil && s.estimates != nil {
			lines, err := s.estimates.LineItemsForJob(ctx, job.ID)
			if err != nil {
				return err
			}
			for i, l := range lines {
				doc.LineItems = append(doc.LineItems, model.CustomerLineItem{
					Position: i, Description: l.Description, Quantity: l.Quantity, UnitPriceCents: l.UnitPriceCents,
				})
			}
		}
		return s.invoices.Create(ctx, doc)
	})
	if err != nil {
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

// CountForJob returns how many invoices, in any status, the job has.
func (s *InvoiceService) CountForJob(ctx context.Context, jobID string) (int64, error) {
	return s.invoices.CountForJob(ctx, jobID)
}
