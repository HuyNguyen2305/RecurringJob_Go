package service_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// memWorkOrders is an in-memory WorkOrderStore with the same guards as the
// repository.
type memWorkOrders struct {
	orders    map[string]*model.WorkOrder
	events    []string
	createErr error
	nextID    int
}

func newMemWorkOrders() *memWorkOrders {
	return &memWorkOrders{orders: map[string]*model.WorkOrder{}}
}

func (m *memWorkOrders) Create(_ context.Context, wo *model.WorkOrder) error {
	m.events = append(m.events, "create")
	if m.createErr != nil {
		return m.createErr
	}
	m.nextID++
	wo.ID = uid(fmt.Sprintf("%02x", m.nextID))
	m.orders[wo.ID] = wo
	return nil
}

func (m *memWorkOrders) Get(_ context.Context, id string) (*model.WorkOrder, error) {
	wo, ok := m.orders[id]
	if !ok {
		return nil, apperror.NotFound("work order not found")
	}
	cp := *wo
	return &cp, nil
}

func (m *memWorkOrders) GetForUpdate(ctx context.Context, id string) (*model.WorkOrder, error) {
	return m.Get(ctx, id)
}

func (m *memWorkOrders) List(context.Context, model.WorkOrderFilter, int, int) ([]model.WorkOrder, error) {
	return nil, nil
}

func (m *memWorkOrders) UpdateContent(_ context.Context, id string, allowedFrom []string, fields map[string]any, tasks []model.WorkOrderTask) (int64, error) {
	wo := m.orders[id]
	if !slices.Contains(allowedFrom, wo.Status) {
		return 0, nil
	}
	if v, ok := fields["notes"]; ok {
		wo.Notes = v.(string)
	}
	if tasks != nil {
		wo.Tasks = tasks
	}
	return 1, nil
}

func (m *memWorkOrders) UpdateStatusGuarded(_ context.Context, id string, allowedFrom []string, updates map[string]any) (int64, error) {
	wo := m.orders[id]
	if !slices.Contains(allowedFrom, wo.Status) {
		return 0, nil
	}
	wo.Status = updates["status"].(string)
	if t, ok := updates["completed_at"].(time.Time); ok {
		wo.CompletedAt = &t
	}
	return 1, nil
}

func (m *memWorkOrders) DeleteGuarded(_ context.Context, id string, allowedFrom []string) (int64, error) {
	wo := m.orders[id]
	if !slices.Contains(allowedFrom, wo.Status) {
		return 0, nil
	}
	delete(m.orders, id)
	return 1, nil
}

func (m *memWorkOrders) LockOccurrence(context.Context, string, time.Time) error {
	m.events = append(m.events, "lock")
	return nil
}

func (m *memWorkOrders) Transaction(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func TestWorkOrderCreate(t *testing.T) {
	ctx := context.Background()
	jobID := uid("a1")
	in := service.WorkOrderInput{Notes: "n", Tasks: []service.TaskInput{{Description: " Clean windows "}, {Description: "Mop"}}}
	job := func() mockJobs { return mockJobs{jobID: withRefs(recJob(jobID, "2026-10-02", recurrenceDaily()))} }

	t.Run("locks, checks availability, then snapshots the job onto the work order", func(t *testing.T) {
		store := newMemWorkOrders()
		occs := &fakeAvailability{events: &store.events}
		s := service.NewWorkOrderService(store, job(), occs)
		wo, err := s.Create(ctx, jobID, time.Date(2026, 10, 5, 14, 30, 0, 0, time.FixedZone("+7", 7*3600)), in)
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"lock", "available", "create"}; !slices.Equal(store.events, want) {
			t.Fatalf("events %v, want %v", store.events, want)
		}
		if wo.Status != service.WOStatusDraft || wo.JobID != jobID || !wo.OccurrenceDate.Equal(dt("2026-10-05")) {
			t.Fatalf("work order %+v", wo)
		}
		if wo.CustomerID != refCustomer || wo.LocationID != refLocation || wo.ServiceTypeID != refService {
			t.Fatalf("references %s %s %s", wo.CustomerID, wo.LocationID, wo.ServiceTypeID)
		}
		if wo.JobSnapshot == nil || wo.JobSnapshot.CustomerName != "Ada" || wo.JobSnapshot.ServiceTypeName != "Window cleaning" {
			t.Fatalf("snapshot %+v", wo.JobSnapshot)
		}
		if len(wo.Tasks) != 2 || wo.Tasks[0].Description != "Clean windows" || wo.Tasks[1].Position != 1 {
			t.Fatalf("tasks %+v", wo.Tasks)
		}
	})

	t.Run("rejections save nothing", func(t *testing.T) {
		boom := errors.New("db down")
		tests := []struct {
			name  string
			job   string
			input service.WorkOrderInput
			occs  error
			want  int
			isErr error
		}{
			{"malformed job id", "nope", in, nil, 400, nil},
			{"blank task", jobID, service.WorkOrderInput{Tasks: []service.TaskInput{{Description: "  "}}}, nil, 400, nil},
			{"not an occurrence", jobID, in, apperror.NotFound("date is not an occurrence of this job"), 404, nil},
			{"occurrence not available", jobID, in, apperror.Conflict("occurrence is canceled"), 409, nil},
			{"availability infrastructure error", jobID, in, boom, 0, boom},
			{"unknown job", uid("ff"), in, nil, 404, nil},
		}
		for _, tt := range tests {
			store := newMemWorkOrders()
			s := service.NewWorkOrderService(store, job(), &fakeAvailability{err: tt.occs})
			_, err := s.Create(ctx, tt.job, dt("2026-10-05"), tt.input)
			if tt.isErr != nil {
				if !errors.Is(err, tt.isErr) {
					t.Errorf("%s: %v", tt.name, err)
				}
			} else if statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if len(store.orders) != 0 {
				t.Errorf("%s: a work order was saved", tt.name)
			}
		}
	})

	t.Run("a duplicate comes back as is", func(t *testing.T) {
		store := newMemWorkOrders()
		dup := apperror.Conflict("a work order already exists for this occurrence")
		store.createErr = dup
		s := service.NewWorkOrderService(store, job(), &fakeAvailability{})
		if _, err := s.Create(ctx, jobID, dt("2026-10-05"), in); !errors.Is(err, dup) {
			t.Fatalf("duplicate: %v", err)
		}
	})
}

func TestWorkOrderStatusTransitions(t *testing.T) {
	all := []string{"draft", "scheduled", "in_progress", "completed", "canceled"}
	allowed := map[string][]string{
		"draft":       {"scheduled", "canceled"},
		"scheduled":   {"in_progress", "canceled"},
		"in_progress": {"completed", "canceled"},
	}
	for _, from := range all {
		for _, to := range all {
			want := slices.Contains(allowed[from], to)
			if got := service.CanTransitionWorkOrder(from, to); got != want {
				t.Errorf("%s -> %s: %v, want %v", from, to, got, want)
			}
		}
	}
	if !service.IsKnownWorkOrderStatus("scheduled") || service.IsKnownWorkOrderStatus("paid") {
		t.Error("known statuses")
	}
}

func TestWorkOrderLifecycle(t *testing.T) {
	ctx := context.Background()
	jobID := uid("a3")
	store := newMemWorkOrders()
	s := service.NewWorkOrderService(store, mockJobs{jobID: withRefs(recJob(jobID, "2026-10-02", recurrenceDaily()))}, &fakeAvailability{})
	create := func(tasks ...service.TaskInput) string {
		wo, err := s.Create(ctx, jobID, dt("2026-10-05"), service.WorkOrderInput{Tasks: tasks})
		if err != nil {
			t.Fatal(err)
		}
		return wo.ID
	}

	t.Run("scheduling needs a task, completing needs every task done", func(t *testing.T) {
		empty := create()
		if _, err := s.ChangeStatus(ctx, empty, service.WOStatusScheduled); statusOf(t, err) != 400 {
			t.Fatalf("schedule without tasks: %v", err)
		}
		id := create(service.TaskInput{Description: "a"})
		for _, to := range []string{service.WOStatusScheduled, service.WOStatusInProgress} {
			if _, err := s.ChangeStatus(ctx, id, to); err != nil {
				t.Fatalf("to %s: %v", to, err)
			}
		}
		if _, err := s.ChangeStatus(ctx, id, service.WOStatusCompleted); statusOf(t, err) != 400 {
			t.Fatalf("complete with an open task: %v", err)
		}
		done := []service.TaskInput{{Description: "a", Done: true}}
		if _, err := s.Update(ctx, id, service.WorkOrderPatch{Tasks: &done}); err != nil {
			t.Fatalf("tick off while in progress: %v", err)
		}
		wo, err := s.ChangeStatus(ctx, id, service.WOStatusCompleted)
		if err != nil || wo.Status != service.WOStatusCompleted || wo.CompletedAt == nil {
			t.Fatalf("complete: %+v %v", wo, err)
		}
	})

	t.Run("a completed work order is final and cannot be edited or deleted", func(t *testing.T) {
		id := create(service.TaskInput{Description: "a", Done: true})
		for _, to := range []string{service.WOStatusScheduled, service.WOStatusInProgress, service.WOStatusCompleted} {
			if _, err := s.ChangeStatus(ctx, id, to); err != nil {
				t.Fatal(err)
			}
		}
		notes := "late"
		if _, err := s.Update(ctx, id, service.WorkOrderPatch{Notes: &notes}); statusOf(t, err) != 409 {
			t.Errorf("edit: %v", err)
		}
		if _, err := s.ChangeStatus(ctx, id, service.WOStatusCanceled); statusOf(t, err) != 409 {
			t.Errorf("cancel: %v", err)
		}
		if err := s.Delete(ctx, id); statusOf(t, err) != 409 {
			t.Errorf("delete: %v", err)
		}
	})

	t.Run("only a draft can be deleted", func(t *testing.T) {
		id := create(service.TaskInput{Description: "a"})
		if err := s.Delete(ctx, id); err != nil {
			t.Fatalf("delete draft: %v", err)
		}
		if _, err := s.Get(ctx, id); statusOf(t, err) != 404 {
			t.Errorf("get after delete: %v", err)
		}
		id = create(service.TaskInput{Description: "a"})
		if _, err := s.ChangeStatus(ctx, id, service.WOStatusScheduled); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, id); statusOf(t, err) != 409 {
			t.Errorf("delete scheduled: %v", err)
		}
	})

	t.Run("bad input", func(t *testing.T) {
		id := create(service.TaskInput{Description: "a"})
		if _, err := s.ChangeStatus(ctx, id, "paid"); statusOf(t, err) != 400 {
			t.Errorf("unknown status: %v", err)
		}
		if _, err := s.Update(ctx, id, service.WorkOrderPatch{}); statusOf(t, err) != 400 {
			t.Errorf("empty patch: %v", err)
		}
		if _, err := s.Get(ctx, "nope"); statusOf(t, err) != 400 {
			t.Errorf("malformed id: %v", err)
		}
	})
}

func TestWorkOrderList(t *testing.T) {
	s := service.NewWorkOrderService(newMemWorkOrders(), mockJobs{}, &fakeAvailability{})
	ctx := context.Background()
	for name, q := range map[string]service.WorkOrderListQuery{
		"unknown status": {Status: "paid"},
		"bad customer":   {CustomerID: "x"},
		"limit too big":  {Limit: service.MaxDocumentLimit + 1},
		"negative":       {Offset: -1},
		"reversed dates": {OccurrenceFrom: dt("2026-10-06"), OccurrenceTo: dt("2026-10-05")},
	} {
		if _, err := s.List(ctx, q); statusOf(t, err) != 400 {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := s.List(ctx, service.WorkOrderListQuery{Status: "draft"}); err != nil {
		t.Errorf("valid list: %v", err)
	}
}
