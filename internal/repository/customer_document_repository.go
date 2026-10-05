package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
)

// CustomerDocumentRepository is the shared data access for estimates and
// invoices, which live in one table. It is never used directly: the
// type-scoped EstimateRepository and InvoiceRepository embed it, and every
// read and write is pinned to that one Type.
type CustomerDocumentRepository struct {
	BaseRepository
	docType string
}

func newCustomerDocumentRepository(db *gorm.DB, docType string) CustomerDocumentRepository {
	return CustomerDocumentRepository{BaseRepository: NewBaseRepository(db), docType: docType}
}

// Create inserts the document and its line items in one transaction. A unique
// violation (a second estimate for a job, a second invoice for an
// occurrence) is a 409.
func (r *CustomerDocumentRepository) Create(ctx context.Context, doc *model.CustomerDocument) error {
	doc.Type = r.docType
	return r.WithSchema(ctx, func(tx *gorm.DB) error {
		// The customer, location and service type are read-only: only the
		// document and its line items are written.
		err := tx.Omit("Customer", "Location", "ServiceType").Create(doc).Error
		if isUniqueViolation(err) {
			if r.docType == model.DocTypeInvoice {
				return apperror.Conflict("an invoice already exists for this occurrence")
			}
			return apperror.Conflict("an estimate already exists for this job")
		}
		return err
	})
}

// Get returns the document with its line items in order, or a NotFound.
func (r *CustomerDocumentRepository) Get(ctx context.Context, id string) (*model.CustomerDocument, error) {
	return r.load(ctx, id, false)
}

// GetForUpdate is Get that also locks the document row until the surrounding
// transaction ends, so a concurrent edit, status change or approval waits
// and then sees the result. Call it inside Transaction; outside one the lock
// is released at once.
func (r *CustomerDocumentRepository) GetForUpdate(ctx context.Context, id string) (*model.CustomerDocument, error) {
	return r.load(ctx, id, true)
}

func (r *CustomerDocumentRepository) load(ctx context.Context, id string, lock bool) (*model.CustomerDocument, error) {
	var doc model.CustomerDocument
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		q := tx.Preload("Customer").Preload("Location").Preload("ServiceType").
			Where("id = ? AND type = ?", id, r.docType)
		if lock {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := q.First(&doc).Error; err != nil {
			return err
		}
		// Loaded separately so the lock above only covers the document row.
		return tx.Where("parent_id = ?", id).Order("position").Find(&doc.LineItems).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.NotFound(r.docType + " not found")
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// List returns documents newest first, optionally filtered by status.
func (r *CustomerDocumentRepository) List(ctx context.Context, status string, limit, offset int) ([]model.CustomerDocument, error) {
	docs := []model.CustomerDocument{}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		q := tx.Where("type = ?", r.docType)
		if status != "" {
			q = q.Where("status = ?", status)
		}
		return q.Order("created_at DESC, id").Limit(limit).Offset(offset).
			Preload("Customer").Preload("Location").Preload("ServiceType").
			Preload("LineItems", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
			Find(&docs).Error
	})
	return docs, err
}

// UpdateContent updates fields and, when items is non-nil, replaces all line
// items, but only while the document's status is still one of allowedFrom
// (optimistic guard). It returns the documents affected; 0 means the
// document changed under the caller.
func (r *CustomerDocumentRepository) UpdateContent(ctx context.Context, id string, allowedFrom []string, fields map[string]any, items []model.CustomerLineItem) (int64, error) {
	var n int64
	fields["updated_at"] = time.Now().UTC() // also set when only line items change
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Model(&model.CustomerDocument{}).
			Where("id = ? AND type = ? AND status IN ?", id, r.docType, allowedFrom).
			Updates(fields)
		if res.Error != nil {
			return res.Error
		}
		n = res.RowsAffected
		if n == 0 || items == nil {
			return nil
		}
		if err := tx.Where("parent_id = ?", id).Delete(&model.CustomerLineItem{}).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		for i := range items {
			items[i].ParentID = id
		}
		return tx.Create(&items).Error
	})
	return n, err
}

// UpdateStatusGuarded applies updates only while the status is still one of
// allowedFrom and returns the rows affected; 0 means it changed under the
// caller.
func (r *CustomerDocumentRepository) UpdateStatusGuarded(ctx context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Model(&model.CustomerDocument{}).
			Where("id = ? AND type = ? AND status IN ?", id, r.docType, allowedFrom).
			Updates(updates)
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}
