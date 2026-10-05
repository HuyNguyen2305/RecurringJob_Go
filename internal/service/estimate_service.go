package service

import (
	"context"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
	"recurringjob/internal/recurrence"
)

// EstimateStore is the storage the estimate service needs.
type EstimateStore interface {
	DocumentStore
	// MarkApproved ties the estimate to its job and sets its status, only
	// while the status is in allowedFrom; it returns the rows affected.
	MarkApproved(ctx context.Context, id string, allowedFrom []string, status, jobID string, snapshot *model.JobSnapshot) (int64, error)
}

// JobCreator creates the job an approved estimate turns into.
type JobCreator interface {
	CreateJob(ctx context.Context, in CreateJobInput) (*model.Job, error)
}

// ApproveEstimateInput describes the job created when an estimate is
// approved. The job's customer, location and service type come from the
// estimate. StartTime is "HH:MM" or "HH:MM:SS".
type ApproveEstimateInput struct {
	Date          time.Time
	StartTime     string
	LengthMinutes int
	Recurrence    *recurrence.Rule
}

// PaidInvoiceChecker tells whether a job has a paid invoice. InvoiceService
// satisfies it.
type PaidInvoiceChecker interface {
	HasPaidForJob(ctx context.Context, jobID string) (bool, error)
}

// EstimateService holds the estimate business rules. Get, List and the shared
// part of ChangeStatus come from the documentCore.
type EstimateService struct {
	*documentCore
	estimates EstimateStore
	jobs      JobCreator
	refs      ReferenceResolver
	invoices  PaidInvoiceChecker
	now       func() time.Time
}

func NewEstimateService(estimates EstimateStore, jobs JobCreator, refs ReferenceResolver) *EstimateService {
	return &EstimateService{
		documentCore: &documentCore{docType: model.DocTypeEstimate, store: estimates},
		estimates:    estimates,
		jobs:         jobs,
		refs:         refs,
		now:          time.Now,
	}
}

// WithClock replaces the clock used to decide what "today" is (for tests).
func (s *EstimateService) WithClock(now func() time.Time) *EstimateService {
	s.now = now
	return s
}

// WithInvoices sets the check that closes an estimate to edits once an invoice
// for its job is paid. Without it that check is skipped.
func (s *EstimateService) WithInvoices(invoices PaidInvoiceChecker) *EstimateService {
	s.invoices = invoices
	return s
}

// Create saves a new draft estimate for a customer's location and a service type.
func (s *EstimateService) Create(ctx context.Context, in EstimateInput) (*model.CustomerDocument, error) {
	doc, err := buildDocument(in.DocumentInput)
	if err != nil {
		return nil, err
	}
	refs, err := s.refs.Resolve(ctx, in.CustomerID, in.LocationID, in.ServiceTypeID)
	if err != nil {
		return nil, err
	}
	doc.CustomerID, doc.LocationID, doc.ServiceTypeID = refs.Customer.ID, refs.Location.ID, refs.ServiceType.ID
	doc.Customer, doc.Location, doc.ServiceType = refs.Customer, refs.Location, refs.ServiceType
	if err := s.estimates.Create(ctx, doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// ChangeStatus is the shared transition, except that approval has its own
// endpoint because it also creates the job.
func (s *EstimateService) ChangeStatus(ctx context.Context, id, to string) (*model.CustomerDocument, error) {
	if to == DocStatusApproved {
		return nil, apperror.Validation("use the approve endpoint to approve an estimate")
	}
	return s.documentCore.ChangeStatus(ctx, id, to)
}

// Approve creates the job from the estimate and marks the estimate approved,
// together in one transaction: either both happen or neither does. The job
// date must be today or later (today is the UTC calendar day).
func (s *EstimateService) Approve(ctx context.Context, id string, in ApproveEstimateInput) (*model.CustomerDocument, error) {
	if err := ValidateID("estimate id", id); err != nil {
		return nil, err
	}
	if civil.Truncate(in.Date).Before(civil.Truncate(s.now())) {
		return nil, apperror.Validation("job date cannot be in the past")
	}
	err := s.estimates.Transaction(ctx, func(ctx context.Context) error {
		// The row lock makes concurrent approvals (or an edit racing an
		// approval) run one after the other; the loser sees the result.
		doc, err := s.estimates.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if !contains(estimateApprovableFrom, doc.Status) {
			return apperror.Conflict("an estimate that is " + doc.Status + " cannot be approved")
		}
		if err := requirePriced(doc, "approving"); err != nil {
			return err
		}
		job, err := s.jobs.CreateJob(ctx, CreateJobInput{
			CustomerID: doc.CustomerID, LocationID: doc.LocationID, ServiceTypeID: doc.ServiceTypeID,
			Date: in.Date, StartTime: in.StartTime, LengthMinutes: in.LengthMinutes, Recurrence: in.Recurrence,
		})
		if err != nil {
			return err
		}
		n, err := s.estimates.MarkApproved(ctx, id, estimateApprovableFrom, DocStatusApproved, job.ID, snapshotOf(job))
		if err != nil {
			return err
		}
		if n == 0 {
			return apperror.Conflict("estimate was changed by another request")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.estimates.Get(ctx, id)
}

// Update edits the estimate while it is draft, sent or approved, until an
// invoice for its job has been paid. Editing an approved estimate changes
// only the estimate: the job and the invoices already issued stay as they are.
// The customer, location and service type can only change while the estimate
// has no job yet.
func (s *EstimateService) Update(ctx context.Context, id string, p DocumentPatch) (*model.CustomerDocument, error) {
	return s.update(ctx, id, p, func(ctx context.Context, doc *model.CustomerDocument, fields map[string]any) error {
		if p.hasRefs() {
			if doc.JobID != nil {
				return apperror.Conflict("this estimate is approved, so its customer, location and service type are fixed by its job")
			}
			if err := s.applyRefs(ctx, doc, p, fields); err != nil {
				return err
			}
		}
		if s.invoices == nil || doc.JobID == nil {
			return nil
		}
		paid, err := s.invoices.HasPaidForJob(ctx, *doc.JobID)
		if err != nil {
			return err
		}
		if paid {
			return apperror.Conflict("an invoice for this estimate's job has been paid, so the estimate can no longer be edited")
		}
		return nil
	})
}

// applyRefs validates the customer, location and service type the patch
// asks for (anything it leaves out keeps the estimate's current value) and
// adds them to the column updates.
func (s *EstimateService) applyRefs(ctx context.Context, doc *model.CustomerDocument, p DocumentPatch, fields map[string]any) error {
	customerID, locationID, serviceTypeID := doc.CustomerID, doc.LocationID, doc.ServiceTypeID
	if p.CustomerID != nil {
		customerID = *p.CustomerID
	}
	if p.LocationID != nil {
		locationID = *p.LocationID
	}
	if p.ServiceTypeID != nil {
		serviceTypeID = *p.ServiceTypeID
	}
	refs, err := s.refs.Resolve(ctx, customerID, locationID, serviceTypeID)
	if err != nil {
		return err
	}
	fields["customer_id"], fields["location_id"], fields["service_type_id"] = refs.Customer.ID, refs.Location.ID, refs.ServiceType.ID
	return nil
}
