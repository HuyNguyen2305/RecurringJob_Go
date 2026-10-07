package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/model"
)

// WorkOrderRepository is the data access for work orders and their tasks.
type WorkOrderRepository struct {
	BaseRepository
}

func NewWorkOrderRepository(db *gorm.DB) *WorkOrderRepository {
	return &WorkOrderRepository{BaseRepository: NewBaseRepository(db)}
}

// Create inserts the work order and its tasks in one transaction. A second
// live work order for the occurrence is a 409.
func (r *WorkOrderRepository) Create(ctx context.Context, wo *model.WorkOrder) error {
	return r.WithSchema(ctx, func(tx *gorm.DB) error {
		err := tx.Omit("Customer", "Location", "ServiceType").Create(wo).Error
		if isForeignKeyViolationOn(err, "job_id") {
			return apperror.Conflict("the job no longer exists")
		}
		if isUniqueViolation(err) {
			return apperror.Conflict("a work order already exists for this occurrence")
		}
		return err
	})
}

// Get returns the work order with its tasks in order, or a NotFound.
func (r *WorkOrderRepository) Get(ctx context.Context, id string) (*model.WorkOrder, error) {
	return r.load(ctx, id, false)
}

// GetForUpdate is Get that also locks the work order row until the surrounding
// transaction ends. Call it inside Transaction.
func (r *WorkOrderRepository) GetForUpdate(ctx context.Context, id string) (*model.WorkOrder, error) {
	return r.load(ctx, id, true)
}

func (r *WorkOrderRepository) load(ctx context.Context, id string, lock bool) (*model.WorkOrder, error) {
	var wo model.WorkOrder
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		q := tx.Preload("Customer").Preload("Location").Preload("ServiceType").Where("id = ?", id)
		if lock {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := q.First(&wo).Error; err != nil {
			return err
		}
		// Loaded separately so the lock above only covers the work order row.
		return tx.Where("work_order_id = ?", id).Order("position").Find(&wo.Tasks).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.NotFound("work order not found")
	}
	if err != nil {
		return nil, err
	}
	return &wo, nil
}

// List returns work orders newest first, narrowed by the filter.
func (r *WorkOrderRepository) List(ctx context.Context, f model.WorkOrderFilter, limit, offset int) ([]model.WorkOrder, error) {
	wos := []model.WorkOrder{}
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		q := tx.Model(&model.WorkOrder{})
		if f.Status != "" {
			q = q.Where("status = ?", f.Status)
		}
		if f.CustomerID != "" {
			q = q.Where("customer_id = ?", f.CustomerID)
		}
		if f.LocationID != "" {
			q = q.Where("location_id = ?", f.LocationID)
		}
		if f.JobID != "" {
			q = q.Where("job_id = ?", f.JobID)
		}
		if f.Q != "" {
			like := "%" + likeEscaper.Replace(f.Q) + "%"
			q = q.Where("(number ILIKE ? OR customer_id IN (SELECT id FROM customers WHERE name ILIKE ?))", like, like)
		}
		if !f.OccurrenceFrom.IsZero() {
			q = q.Where("occurrence_date >= ?::date", civil.Format(f.OccurrenceFrom))
		}
		if !f.OccurrenceTo.IsZero() {
			q = q.Where("occurrence_date <= ?::date", civil.Format(f.OccurrenceTo))
		}
		return q.Order("created_at DESC, id").Limit(limit).Offset(offset).
			Preload("Customer").Preload("Location").Preload("ServiceType").
			Preload("Tasks", func(db *gorm.DB) *gorm.DB { return db.Order("position") }).
			Find(&wos).Error
	})
	return wos, err
}

// UpdateContent updates fields and, when tasks is non-nil, replaces all tasks,
// but only while the status is still one of allowedFrom (optimistic guard). It
// returns the rows affected; 0 means the work order changed under the caller.
func (r *WorkOrderRepository) UpdateContent(ctx context.Context, id string, allowedFrom []string, fields map[string]any, tasks []model.WorkOrderTask) (int64, error) {
	var n int64
	fields["updated_at"] = time.Now().UTC() // also set when only tasks change
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Model(&model.WorkOrder{}).Where("id = ? AND status IN ?", id, allowedFrom).Updates(fields)
		if res.Error != nil {
			return res.Error
		}
		n = res.RowsAffected
		if n == 0 || tasks == nil {
			return nil
		}
		if err := tx.Where("work_order_id = ?", id).Delete(&model.WorkOrderTask{}).Error; err != nil {
			return err
		}
		if len(tasks) == 0 {
			return nil
		}
		for i := range tasks {
			tasks[i].WorkOrderID = id
		}
		return tx.Create(&tasks).Error
	})
	return n, err
}

// UpdateStatusGuarded applies updates only while the status is still one of
// allowedFrom and returns the rows affected; 0 means it changed under the
// caller.
func (r *WorkOrderRepository) UpdateStatusGuarded(ctx context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Model(&model.WorkOrder{}).Where("id = ? AND status IN ?", id, allowedFrom).Updates(updates)
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}

// DeleteGuarded deletes the work order (its tasks go with it) only while its
// status is still one of allowedFrom and returns the rows affected.
func (r *WorkOrderRepository) DeleteGuarded(ctx context.Context, id string, allowedFrom []string) (int64, error) {
	var n int64
	err := r.WithSchema(ctx, func(tx *gorm.DB) error {
		res := tx.Where("id = ? AND status IN ?", id, allowedFrom).Delete(&model.WorkOrder{})
		n = res.RowsAffected
		return res.Error
	})
	return n, err
}
