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
// approved, only while its status is still one of allowedFrom. It returns the
// rows affected; 0 means the estimate changed under the caller.
func (r *EstimateRepository) MarkApproved(ctx context.Context, id string, allowedFrom []string, status, jobID string, snapshot *model.JobSnapshot) (int64, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return 0, err
	}
	return r.UpdateStatusGuarded(ctx, id, allowedFrom, map[string]any{
		"status":       status,
		"job_id":       jobID,
		"job_snapshot": string(raw),
	})
}
