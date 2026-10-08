package service_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

func (m *memWorkOrders) forOccurrence(jobID string, date time.Time, statuses []string) []*model.WorkOrder {
	var out []*model.WorkOrder
	for _, wo := range m.orders {
		if wo.JobID == jobID && wo.OccurrenceDate.Equal(date) && slices.Contains(statuses, wo.Status) {
			out = append(out, wo)
		}
	}
	slices.SortFunc(out, func(a, b *model.WorkOrder) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (m *memWorkOrders) UpdateStatusForOccurrence(_ context.Context, jobID string, date time.Time, allowedFrom []string, status string) (int64, error) {
	found := m.forOccurrence(jobID, date, allowedFrom)
	for _, wo := range found {
		wo.Status = status
	}
	return int64(len(found)), nil
}

func (m *memWorkOrders) MoveForOccurrence(_ context.Context, jobID string, from, to time.Time, statuses []string) (int64, error) {
	found := m.forOccurrence(jobID, from, statuses)
	for _, wo := range found {
		wo.OccurrenceDate = to
	}
	return int64(len(found)), nil
}

func (m *memWorkOrders) IDsForOccurrence(_ context.Context, jobID string, date time.Time, statuses []string) ([]string, error) {
	ids := []string{}
	for _, wo := range m.forOccurrence(jobID, date, statuses) {
		ids = append(ids, wo.ID)
	}
	return ids, nil
}

func (m *memWorkOrders) CountForJob(_ context.Context, jobID string) (int64, error) {
	var n int64
	for _, wo := range m.orders {
		if wo.JobID == jobID {
			n++
		}
	}
	return n, nil
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

	t.Run("past draft the tasks cannot be emptied, so nothing completes without a task", func(t *testing.T) {
		none := []service.TaskInput{}
		id := create(service.TaskInput{Description: "a"})
		// A draft may be emptied; it cannot be scheduled that way.
		if _, err := s.Update(ctx, id, service.WorkOrderPatch{Tasks: &none}); err != nil {
			t.Fatalf("empty a draft: %v", err)
		}
		if _, err := s.ChangeStatus(ctx, id, service.WOStatusScheduled); statusOf(t, err) != 400 {
			t.Fatalf("schedule an empty draft: %v", err)
		}
		one := []service.TaskInput{{Description: "a"}}
		if _, err := s.Update(ctx, id, service.WorkOrderPatch{Tasks: &one}); err != nil {
			t.Fatal(err)
		}
		for _, to := range []string{service.WOStatusScheduled, service.WOStatusInProgress} {
			if _, err := s.ChangeStatus(ctx, id, to); err != nil {
				t.Fatalf("to %s: %v", to, err)
			}
			if _, err := s.Update(ctx, id, service.WorkOrderPatch{Tasks: &none}); statusOf(t, err) != 400 {
				t.Fatalf("emptying while %s: %v", to, err)
			}
			if got, _ := s.Get(ctx, id); len(got.Tasks) != 1 {
				t.Fatalf("a refused edit changed the tasks: %+v", got.Tasks)
			}
		}
		// Even if the store somehow has no tasks, completing is refused.
		store.orders[id].Tasks = nil
		if _, err := s.ChangeStatus(ctx, id, service.WOStatusCompleted); statusOf(t, err) != 400 {
			t.Fatalf("complete with no tasks: %v", err)
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

func TestWorkOrderFollowsItsOccurrence(t *testing.T) {
	ctx := context.Background()
	jobID := uid("a4")
	store := newMemWorkOrders()
	s := service.NewWorkOrderService(store, mockJobs{jobID: withRefs(recJob(jobID, "2026-10-02", recurrenceDaily()))}, &fakeAvailability{})
	day := dt("2026-10-05")
	// seed creates one work order per status on the date, setting the status
	// straight on the store. The fake does not model the one-live-per-occurrence
	// index, so several can share a date.
	seed := func(date time.Time, statuses ...string) []string {
		var ids []string
		for _, st := range statuses {
			wo, err := s.Create(ctx, jobID, date, service.WorkOrderInput{})
			if err != nil {
				t.Fatal(err)
			}
			store.orders[wo.ID].Status = st
			ids = append(ids, wo.ID)
		}
		return ids
	}

	t.Run("cancel takes the open work orders and leaves a completed one", func(t *testing.T) {
		ids := seed(day, service.WOStatusDraft, service.WOStatusScheduled, service.WOStatusInProgress, service.WOStatusCompleted)
		n, err := s.CancelOpenForOccurrence(ctx, jobID, time.Date(2026, 10, 5, 22, 0, 0, 0, time.FixedZone("+7", 7*3600)))
		if err != nil || n != 3 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		for i, want := range []string{"canceled", "canceled", "canceled", "completed"} {
			if got := store.orders[ids[i]].Status; got != want {
				t.Errorf("work order %d is %s, want %s", i, got, want)
			}
		}
		kept, _ := s.IDsForOccurrence(ctx, jobID, day, []string{service.WOStatusCompleted})
		if !slices.Equal(kept, []string{ids[3]}) {
			t.Errorf("kept %v, want %v", kept, ids[3:])
		}
	})

	t.Run("move takes only the work orders that have not started", func(t *testing.T) {
		other := dt("2026-10-12")
		ids := seed(other, service.WOStatusDraft, service.WOStatusScheduled, service.WOStatusInProgress, service.WOStatusCompleted, service.WOStatusCanceled)
		to := dt("2026-10-13")
		n, err := s.MoveOpenForOccurrence(ctx, jobID, other, to)
		if err != nil || n != 2 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		for i, want := range []time.Time{to, to, other, other, other} {
			if got := store.orders[ids[i]].OccurrenceDate; !got.Equal(want) {
				t.Errorf("work order %d is on %s, want %s", i, got.Format("2006-01-02"), want.Format("2006-01-02"))
			}
		}
		left, _ := s.IDsForOccurrence(ctx, jobID, other, []string{service.WOStatusInProgress, service.WOStatusCompleted})
		if !slices.Equal(left, ids[2:4]) {
			t.Errorf("left behind %v, want %v", left, ids[2:4])
		}
	})

	t.Run("another occurrence or job is never touched", func(t *testing.T) {
		store := newMemWorkOrders()
		s := service.NewWorkOrderService(store, mockJobs{jobID: withRefs(recJob(jobID, "2026-10-02", recurrenceDaily()))}, &fakeAvailability{})
		wo, err := s.Create(ctx, jobID, dt("2026-10-06"), service.WorkOrderInput{})
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := s.CancelOpenForOccurrence(ctx, jobID, dt("2026-10-07")); n != 0 {
			t.Errorf("canceled %d on another date", n)
		}
		if n, _ := s.CancelOpenForOccurrence(ctx, uid("ff"), dt("2026-10-06")); n != 0 {
			t.Errorf("canceled %d for another job", n)
		}
		if store.orders[wo.ID].Status != service.WOStatusDraft {
			t.Errorf("status %s", store.orders[wo.ID].Status)
		}
		if n, err := s.CountForJob(ctx, jobID); err != nil || n != 1 {
			t.Errorf("count %d err=%v", n, err)
		}
	})
}
