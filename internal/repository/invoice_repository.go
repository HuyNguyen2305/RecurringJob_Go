package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

// InvoiceRepository is the invoice view of customer_documents.
type InvoiceRepository struct {
	CustomerDocumentRepository
}

func NewInvoiceRepository(db *gorm.DB) *InvoiceRepository {
	return &InvoiceRepository{CustomerDocumentRepository: newCustomerDocumentRepository(db, model.DocTypeInvoice)}
}

// UpdateStatusForOccurrence moves the occurrence's invoices that are in one of
// allowedFrom to status and returns how many changed. The date is compared as
// a plain YYYY-MM-DD so the session time zone cannot shift it.
func (r *InvoiceRepository) UpdateStatusForOccurrence(ctx context.Context, jobID string, date time.Time, allowedFrom []string, status string) (int64, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Model(&model.CustomerDocument{}).
			Where("type = ? AND job_id = ? AND occurrence_date = ?::date AND status IN ?", r.docType, jobID, civil.Format(date), allowedFrom).
			Updates(map[string]any{"status": status})
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}

// MoveUnpaidForOccurrence moves the draft and sent invoices of the occurrence
// on from to the occurrence on to and returns how many moved. Paid and void
// invoices stay where they are.
func (r *InvoiceRepository) MoveUnpaidForOccurrence(ctx context.Context, jobID string, from, to time.Time) (int64, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Model(&model.CustomerDocument{}).
			Where("type = ? AND job_id = ? AND occurrence_date = ?::date AND status IN ?", r.docType, jobID, civil.Format(from), []string{"draft", "sent"}).
			Updates(map[string]any{"occurrence_date": civil.Format(to), "updated_at": time.Now().UTC()})
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}

// IDsForOccurrence returns the ids of the occurrence's invoices in the given status.
func (r *InvoiceRepository) IDsForOccurrence(ctx context.Context, jobID string, date time.Time, status string) ([]string, error) {
	ids := []string{}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Model(&model.CustomerDocument{}).
			Where("type = ? AND job_id = ? AND occurrence_date = ?::date AND status = ?", r.docType, jobID, civil.Format(date), status).
			Order("created_at, id").Pluck("id", &ids).Error
	})
	return ids, err
}

// CountForJob returns how many invoices, in any status, the job has.
func (r *InvoiceRepository) CountForJob(ctx context.Context, jobID string) (int64, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Model(&model.CustomerDocument{}).Where("type = ? AND job_id = ?", r.docType, jobID).Count(&n).Error
	})
	return n, err
}

// ExistsForJob reports whether the job has an invoice in the given status.
func (r *InvoiceRepository) ExistsForJob(ctx context.Context, jobID, status string) (bool, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Model(&model.CustomerDocument{}).
			Where("type = ? AND job_id = ? AND status = ?", r.docType, jobID, status).
			Limit(1).Count(&n).Error
	})
	return n > 0, err
}
