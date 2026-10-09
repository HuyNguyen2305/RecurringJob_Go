package repository

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"

	"recurringjob/internal/model"
)

// EstimateRepository is the estimate view of customer_documents.
type EstimateRepository struct {
	CustomerDocumentRepository
}

func NewEstimateRepository(db *gorm.DB) *EstimateRepository {
	return &EstimateRepository{CustomerDocumentRepository: newCustomerDocumentRepository(db, model.DocTypeEstimate)}
}

// MarkApproved ties the estimate to the job created from it and sets it
// approved, only while its status is still one of allowedFrom. An estimate
// that was never marked sent is stamped as sent now: the customer had it.
// It returns the rows affected; 0 means the estimate changed under the caller.
func (r *EstimateRepository) MarkApproved(ctx context.Context, id string, allowedFrom []string, status, jobID string, snapshot *model.JobSnapshot) (int64, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return 0, err
	}
	return r.UpdateStatusGuarded(ctx, id, allowedFrom, map[string]any{
		"status":       status,
		"job_id":       jobID,
		"job_snapshot": string(raw),
		"sent_at":      gorm.Expr("COALESCE(sent_at, now())"),
	})
}

// MarkReopened undoes MarkApproved: the estimate loses its job and snapshot
// and gets status, only while its status is still one of allowedFrom. It
// returns the rows affected.
func (r *EstimateRepository) MarkReopened(ctx context.Context, id string, allowedFrom []string, status string) (int64, error) {
	return r.UpdateStatusGuarded(ctx, id, allowedFrom, map[string]any{
		"status":       status,
		"job_id":       nil,
		"job_snapshot": nil,
	})
}

// LineItemsForJob returns the line items of the job's estimate in position
// order; empty (never nil) when the job has no estimate.
func (r *EstimateRepository) LineItemsForJob(ctx context.Context, jobID string) ([]model.CustomerLineItem, error) {
	items := []model.CustomerLineItem{}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		estimates := tx.Model(&model.CustomerDocument{}).Select("id").Where("type = ? AND job_id = ?", r.docType, jobID)
		return tx.Where("parent_id IN (?)", estimates).Order("position").Find(&items).Error
	})
	return items, err
}

// SaveRevision stores the estimate's current content as the given revision.
func (r *EstimateRepository) SaveRevision(ctx context.Context, rev *model.DocumentRevision) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error { return tx.Create(rev).Error })
}

// ListRevisions returns the stored revisions of the estimate, newest first;
// never nil.
func (r *EstimateRepository) ListRevisions(ctx context.Context, id string) ([]model.DocumentRevision, error) {
	out := []model.DocumentRevision{}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		return tx.Where("document_id = ?", id).Order("revision DESC").Find(&out).Error
	})
	return out, err
}
