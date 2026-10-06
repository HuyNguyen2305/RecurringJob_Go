package service

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

const (
	DefaultDocumentLimit = 50
	MaxDocumentLimit     = 200
)

// DocumentStore is the storage both document services need. The estimate and
// invoice repositories satisfy it, each pinned to its own document type.
type DocumentStore interface {
	Create(ctx context.Context, doc *model.CustomerDocument) error
	Get(ctx context.Context, id string) (*model.CustomerDocument, error)
	// GetForUpdate is Get that locks the row until the surrounding
	// transaction ends.
	GetForUpdate(ctx context.Context, id string) (*model.CustomerDocument, error)
	List(ctx context.Context, f model.DocumentFilter, limit, offset int) ([]model.CustomerDocument, error)
	// UpdateContent updates fields (and replaces line items when items is
	// non-nil) only while the status is in allowedFrom; it returns the rows
	// affected.
	UpdateContent(ctx context.Context, id string, allowedFrom []string, fields map[string]any, items []model.CustomerLineItem) (int64, error)
	// UpdateStatusGuarded updates only while the status is in allowedFrom and
	// returns the rows affected.
	UpdateStatusGuarded(ctx context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error)
	// DeleteGuarded deletes only while the status is in allowedFrom and
	// returns the rows affected.
	DeleteGuarded(ctx context.Context, id string, allowedFrom []string) (int64, error)
	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// DocumentListQuery filters and pages a document list.
type DocumentListQuery struct {
	Status         string
	CustomerID     string
	LocationID     string
	JobID          string
	Q              string    // customer name or document number
	OccurrenceFrom time.Time // invoices only
	OccurrenceTo   time.Time // invoices only
	Limit          int
	Offset         int
}

// maxSearchLen bounds the free-text filter.
const maxSearchLen = 100

// documentCore is the behaviour estimates and invoices share: reading,
// editing and moving through the status transitions. The type-specific
// services embed it.
//
// Every change loads the document with a row lock and checks and writes in
// that one transaction, so two requests on the same document (an edit and a
// send, say) run one after the other and the second sees the first's result.
type documentCore struct {
	docType string
	store   DocumentStore
}

func snapshotOf(job *model.Job) *model.JobSnapshot {
	s := &model.JobSnapshot{
		ID: job.ID, Date: civil.Format(job.Date), StartTime: job.StartTime, LengthMinutes: job.LengthMinutes,
		Status: job.Status, CustomerID: job.CustomerID, LocationID: job.LocationID, ServiceTypeID: job.ServiceTypeID,
		Recurrence: job.Recurrence,
	}
	if job.Customer != nil {
		s.CustomerName = job.Customer.Name
	}
	if job.Location != nil {
		s.LocationAddress = job.Location.AddressLine1
	}
	if job.ServiceType != nil {
		s.ServiceTypeName = job.ServiceType.Name
	}
	return s
}

// requirePriced rejects a document that has no priced content: a document
// with no line items, or only free ones, has nothing to bill.
func requirePriced(doc *model.CustomerDocument, action string) error {
	if doc.TotalCents() <= 0 {
		return apperror.Validation("add at least one priced line item before " + action)
	}
	return nil
}

// Get returns one document with its line items.
func (c *documentCore) Get(ctx context.Context, id string) (*model.CustomerDocument, error) {
	if err := ValidateID(c.docType+" id", id); err != nil {
		return nil, err
	}
	return c.store.Get(ctx, id)
}

// List returns documents newest first.
func (c *documentCore) List(ctx context.Context, q DocumentListQuery) ([]model.CustomerDocument, error) {
	if q.Status != "" && !IsKnownDocumentStatus(c.docType, q.Status) {
		return nil, apperror.Validation("unknown status")
	}
	for name, id := range map[string]string{"customerId": q.CustomerID, "locationId": q.LocationID, "jobId": q.JobID} {
		if id != "" {
			if err := ValidateID(name, id); err != nil {
				return nil, err
			}
		}
	}
	if utf8.RuneCountInString(q.Q) > maxSearchLen {
		return nil, bad("q must be at most %d characters", maxSearchLen)
	}
	hasFrom, hasTo := !q.OccurrenceFrom.IsZero(), !q.OccurrenceTo.IsZero()
	if (hasFrom || hasTo) && c.docType != model.DocTypeInvoice {
		return nil, apperror.Validation("occurrenceFrom and occurrenceTo only apply to invoices")
	}
	if hasFrom && hasTo && q.OccurrenceFrom.After(q.OccurrenceTo) {
		return nil, apperror.Validation("occurrenceFrom cannot be after occurrenceTo")
	}
	if q.Limit == 0 {
		q.Limit = DefaultDocumentLimit
	}
	if q.Limit < 1 || q.Limit > MaxDocumentLimit {
		return nil, bad("limit must be between 1 and %d", MaxDocumentLimit)
	}
	if q.Offset < 0 {
		return nil, apperror.Validation("offset cannot be negative")
	}
	return c.store.List(ctx, model.DocumentFilter{
		Status: q.Status, CustomerID: q.CustomerID, LocationID: q.LocationID, JobID: q.JobID, Q: strings.TrimSpace(q.Q),
		OccurrenceFrom: q.OccurrenceFrom, OccurrenceTo: q.OccurrenceTo,
	}, q.Limit, q.Offset)
}

// Delete removes a draft document (and its line items). Anything past draft
// is already in front of the customer, so it is declined or voided instead.
func (c *documentCore) Delete(ctx context.Context, id string) error {
	if err := ValidateID(c.docType+" id", id); err != nil {
		return err
	}
	return c.store.Transaction(ctx, func(ctx context.Context) error {
		doc, err := c.store.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if doc.Status != DocStatusDraft {
			return apperror.Conflict("only a draft " + c.docType + " can be deleted; this one is " + doc.Status)
		}
		n, err := c.store.DeleteGuarded(ctx, id, []string{DocStatusDraft})
		if err != nil {
			return err
		}
		if n == 0 {
			return apperror.Conflict(c.docType + " was changed by another request")
		}
		return nil
	})
}

// statusTimestamp is the column that records when a document reached status.
var statusTimestamp = map[string]string{DocStatusSent: "sent_at", DocStatusPaid: "paid_at", DocStatusRefunded: "refunded_at"}

// Update edits the document: the given fields change and a given line-item
// list replaces the old one. Only the statuses in DocumentEditableFrom allow
// it (409 otherwise). beforeEdit, when set, runs on the locked document and
// can veto the edit with its own error or add to the column updates.
func (c *documentCore) update(ctx context.Context, id string, p DocumentPatch, beforeEdit func(ctx context.Context, doc *model.CustomerDocument, fields map[string]any) error) (*model.CustomerDocument, error) {
	if err := ValidateID(c.docType+" id", id); err != nil {
		return nil, err
	}
	fields, items, err := buildPatch(p)
	if err != nil {
		return nil, err
	}
	editable := DocumentEditableFrom(c.docType)

	err = c.store.Transaction(ctx, func(ctx context.Context) error {
		doc, err := c.store.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if !contains(editable, doc.Status) {
			return apperror.Conflict("this " + c.docType + " is " + doc.Status + " and can no longer be edited")
		}
		if beforeEdit != nil {
			if err := beforeEdit(ctx, doc, fields); err != nil {
				return err
			}
		}
		// Past draft the document is already in front of the customer, so it
		// must keep something to bill.
		if doc.Status != DocStatusDraft && p.LineItems != nil {
			if err := requirePriced(&model.CustomerDocument{LineItems: items}, "saving a "+doc.Status+" "+c.docType); err != nil {
				return err
			}
		}
		n, err := c.store.UpdateContent(ctx, id, editable, fields, items)
		if err != nil {
			return err
		}
		if n == 0 {
			return apperror.Conflict(c.docType + " was changed by another request")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c.store.Get(ctx, id)
}

// Update edits the document; see update.
func (c *documentCore) Update(ctx context.Context, id string, p DocumentPatch) (*model.CustomerDocument, error) {
	return c.update(ctx, id, p, nil)
}

// ChangeStatus moves the document to a new status if the transition is
// allowed. Sending needs a priced line item.
func (c *documentCore) ChangeStatus(ctx context.Context, id, to string) (*model.CustomerDocument, error) {
	if err := ValidateID(c.docType+" id", id); err != nil {
		return nil, err
	}
	if !IsKnownDocumentStatus(c.docType, to) {
		return nil, apperror.Validation("unknown status")
	}
	err := c.store.Transaction(ctx, func(ctx context.Context) error {
		doc, err := c.store.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if !CanTransitionDocument(c.docType, doc.Status, to) {
			return apperror.Conflict("cannot change an " + c.docType + " from " + doc.Status + " to " + to)
		}
		if to == DocStatusSent {
			if err := requirePriced(doc, "sending"); err != nil {
				return err
			}
		}
		updates := map[string]any{"status": to}
		if col, ok := statusTimestamp[to]; ok {
			updates[col] = time.Now().UTC()
		}
		n, err := c.store.UpdateStatusGuarded(ctx, id, DocumentAllowedFrom(c.docType, to), updates)
		if err != nil {
			return err
		}
		if n == 0 {
			return apperror.Conflict(c.docType + " was changed by another request")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c.store.Get(ctx, id)
}
