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

// MaxWorkOrderTasks bounds the tasks of one work order.
const MaxWorkOrderTasks = 100

// WorkOrderStore is the storage the work order service needs.
type WorkOrderStore interface {
	Create(ctx context.Context, wo *model.WorkOrder) error
	Get(ctx context.Context, id string) (*model.WorkOrder, error)
	// GetForUpdate is Get that locks the row until the surrounding transaction ends.
	GetForUpdate(ctx context.Context, id string) (*model.WorkOrder, error)
	List(ctx context.Context, f model.WorkOrderFilter, limit, offset int) ([]model.WorkOrder, error)
	// UpdateContent updates fields (and replaces tasks when tasks is non-nil)
	// only while the status is in allowedFrom; it returns the rows affected.
	UpdateContent(ctx context.Context, id string, allowedFrom []string, fields map[string]any, tasks []model.WorkOrderTask) (int64, error)
	// UpdateStatusGuarded updates only while the status is in allowedFrom and
	// returns the rows affected.
	UpdateStatusGuarded(ctx context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error)
	// DeleteGuarded deletes only while the status is in allowedFrom and returns
	// the rows affected.
	DeleteGuarded(ctx context.Context, id string, allowedFrom []string) (int64, error)
	// LockOccurrence takes the transaction-scoped lock of one occurrence; call
	// it inside Transaction.
	LockOccurrence(ctx context.Context, jobID string, date time.Time) error
	Transaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// TaskInput is one task of a work order as submitted.
type TaskInput struct {
	Description string
	Done        bool
}

// WorkOrderInput is the content of a new work order.
type WorkOrderInput struct {
	Notes string
	Tasks []TaskInput
}

// WorkOrderPatch is a partial edit; nil fields stay unchanged and a non-nil
// Tasks replaces every task.
type WorkOrderPatch struct {
	Notes *string
	Tasks *[]TaskInput
}

// WorkOrderListQuery filters and pages a work order list.
type WorkOrderListQuery struct {
	Status         string
	CustomerID     string
	LocationID     string
	JobID          string
	Q              string // customer name or work order number
	OccurrenceFrom time.Time
	OccurrenceTo   time.Time
	Limit          int
	Offset         int
}

// WorkOrderService holds the work order business rules.
type WorkOrderService struct {
	orders WorkOrderStore
	jobs   JobGetter
	occs   OccurrenceAvailability
}

func NewWorkOrderService(orders WorkOrderStore, jobs JobGetter, occs OccurrenceAvailability) *WorkOrderService {
	return &WorkOrderService{orders: orders, jobs: jobs, occs: occs}
}

func buildTasks(in []TaskInput) ([]model.WorkOrderTask, error) {
	if len(in) > MaxWorkOrderTasks {
		return nil, bad("a work order can have at most %d tasks", MaxWorkOrderTasks)
	}
	out := make([]model.WorkOrderTask, 0, len(in))
	for i, t := range in {
		desc := strings.TrimSpace(t.Description)
		switch {
		case desc == "":
			return nil, bad("tasks[%d].description is required", i)
		case utf8.RuneCountInString(desc) > MaxDescriptionLen:
			return nil, bad("tasks[%d].description must be at most %d characters", i, MaxDescriptionLen)
		}
		out = append(out, model.WorkOrderTask{Position: i, Description: desc, Done: t.Done})
	}
	return out, nil
}

// Create saves a draft work order for one occurrence of a job. The occurrence
// must exist and be available (404 / 409 otherwise); a second live work order
// for the same occurrence is a 409 (a canceled one does not count). The job is
// snapshotted onto the work order.
func (s *WorkOrderService) Create(ctx context.Context, jobID string, date time.Time, in WorkOrderInput) (*model.WorkOrder, error) {
	if err := ValidateID("job id", jobID); err != nil {
		return nil, err
	}
	notes, err := validateNotes(in.Notes)
	if err != nil {
		return nil, err
	}
	tasks, err := buildTasks(in.Tasks)
	if err != nil {
		return nil, err
	}
	date = civil.Truncate(date)
	wo := &model.WorkOrder{Status: WOStatusDraft, Notes: notes, Tasks: tasks, OccurrenceDate: date}
	// The occurrence lock makes the availability check and the insert one step
	// with respect to a status change of the same occurrence.
	err = s.orders.Transaction(ctx, func(ctx context.Context) error {
		if err := s.orders.LockOccurrence(ctx, jobID, date); err != nil {
			return err
		}
		if err := s.occs.AvailableFor(ctx, jobID, date); err != nil {
			return err
		}
		job, err := s.jobs.GetJob(ctx, jobID)
		if err != nil {
			return err
		}
		wo.JobID = job.ID
		wo.CustomerID, wo.LocationID, wo.ServiceTypeID = job.CustomerID, job.LocationID, job.ServiceTypeID
		wo.Customer, wo.Location, wo.ServiceType = job.Customer, job.Location, job.ServiceType
		wo.JobSnapshot = snapshotOf(job)
		return s.orders.Create(ctx, wo)
	})
	if err != nil {
		return nil, err
	}
	return wo, nil
}

// Get returns one work order with its tasks.
func (s *WorkOrderService) Get(ctx context.Context, id string) (*model.WorkOrder, error) {
	if err := ValidateID("work order id", id); err != nil {
		return nil, err
	}
	return s.orders.Get(ctx, id)
}

// List returns work orders newest first.
func (s *WorkOrderService) List(ctx context.Context, q WorkOrderListQuery) ([]model.WorkOrder, error) {
	if q.Status != "" && !IsKnownWorkOrderStatus(q.Status) {
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
	if !q.OccurrenceFrom.IsZero() && !q.OccurrenceTo.IsZero() && q.OccurrenceFrom.After(q.OccurrenceTo) {
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
	return s.orders.List(ctx, model.WorkOrderFilter{
		Status: q.Status, CustomerID: q.CustomerID, LocationID: q.LocationID, JobID: q.JobID, Q: strings.TrimSpace(q.Q),
		OccurrenceFrom: q.OccurrenceFrom, OccurrenceTo: q.OccurrenceTo,
	}, q.Limit, q.Offset)
}

// Update edits notes and tasks while the work order is draft, scheduled or in
// progress (409 otherwise). A given task list replaces all tasks.
func (s *WorkOrderService) Update(ctx context.Context, id string, p WorkOrderPatch) (*model.WorkOrder, error) {
	if err := ValidateID("work order id", id); err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if p.Notes != nil {
		v, err := validateNotes(*p.Notes)
		if err != nil {
			return nil, err
		}
		fields["notes"] = v
	}
	var tasks []model.WorkOrderTask
	if p.Tasks != nil {
		var err error
		if tasks, err = buildTasks(*p.Tasks); err != nil {
			return nil, err
		}
	}
	if len(fields) == 0 && p.Tasks == nil {
		return nil, apperror.Validation("nothing to update")
	}
	err := s.orders.Transaction(ctx, func(ctx context.Context) error {
		wo, err := s.orders.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if !contains(workOrderEditableFrom, wo.Status) {
			return apperror.Conflict("this work order is " + wo.Status + " and can no longer be edited")
		}
		n, err := s.orders.UpdateContent(ctx, id, workOrderEditableFrom, fields, tasks)
		if err != nil {
			return err
		}
		if n == 0 {
			return apperror.Conflict("work order was changed by another request")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.orders.Get(ctx, id)
}

// ChangeStatus moves the work order to a new status if the transition is
// allowed. Completing needs at least one task, all of them done.
func (s *WorkOrderService) ChangeStatus(ctx context.Context, id, to string) (*model.WorkOrder, error) {
	if err := ValidateID("work order id", id); err != nil {
		return nil, err
	}
	if !IsKnownWorkOrderStatus(to) {
		return nil, apperror.Validation("unknown status")
	}
	err := s.orders.Transaction(ctx, func(ctx context.Context) error {
		wo, err := s.orders.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if !CanTransitionWorkOrder(wo.Status, to) {
			return apperror.Conflict("cannot change a work order from " + wo.Status + " to " + to)
		}
		if to == WOStatusScheduled && len(wo.Tasks) == 0 {
			return apperror.Validation("add at least one task before scheduling")
		}
		if to == WOStatusCompleted {
			for i, t := range wo.Tasks {
				if !t.Done {
					return bad("task %d is not done yet", i+1)
				}
			}
		}
		updates := map[string]any{"status": to}
		if to == WOStatusCompleted {
			updates["completed_at"] = time.Now().UTC()
		}
		n, err := s.orders.UpdateStatusGuarded(ctx, id, WorkOrderAllowedFrom(to), updates)
		if err != nil {
			return err
		}
		if n == 0 {
			return apperror.Conflict("work order was changed by another request")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.orders.Get(ctx, id)
}

// Delete removes a draft work order (and its tasks). Anything past draft is
// already scheduled, so it is canceled instead.
func (s *WorkOrderService) Delete(ctx context.Context, id string) error {
	if err := ValidateID("work order id", id); err != nil {
		return err
	}
	return s.orders.Transaction(ctx, func(ctx context.Context) error {
		wo, err := s.orders.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if wo.Status != WOStatusDraft {
			return apperror.Conflict("only a draft work order can be deleted; this one is " + wo.Status)
		}
		n, err := s.orders.DeleteGuarded(ctx, id, []string{WOStatusDraft})
		if err != nil {
			return err
		}
		if n == 0 {
			return apperror.Conflict("work order was changed by another request")
		}
		return nil
	})
}
