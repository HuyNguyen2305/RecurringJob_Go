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
