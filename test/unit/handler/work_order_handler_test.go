package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/handler"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// fakeWorkOrders records what the work order handler passes down.
type fakeWorkOrders struct {
	wo  *model.WorkOrder
	err error

	createJob  string
	createDate time.Time
	created    service.WorkOrderInput
	getID      string
	listQ      service.WorkOrderListQuery
	updateID   string
	patch      service.WorkOrderPatch
	statusID   string
	statusTo   string
	deleteID   string
	called     bool
}

func newFakeWorkOrders() *fakeWorkOrders {
	return &fakeWorkOrders{wo: &model.WorkOrder{
		ID: "wo-1", Number: "WO-000001", Status: "draft", JobID: "job-1", OccurrenceDate: civil.New(2026, 10, 5),
		Customer: &model.Customer{ID: "c1", Name: "Ada"},
		Tasks:    []model.WorkOrderTask{{Description: "Clean windows"}, {Description: "Mop floor", Done: true}},
	}}
}

func (f *fakeWorkOrders) result() (*model.WorkOrder, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return f.wo, nil
}

func (f *fakeWorkOrders) Create(_ context.Context, jobID string, date time.Time, in service.WorkOrderInput) (*model.WorkOrder, error) {
	f.createJob, f.createDate, f.created = jobID, date, in
	return f.result()
}

func (f *fakeWorkOrders) Get(_ context.Context, id string) (*model.WorkOrder, error) {
	f.getID = id
	return f.result()
}

func (f *fakeWorkOrders) List(_ context.Context, q service.WorkOrderListQuery) ([]model.WorkOrder, error) {
	f.listQ = q
	wo, err := f.result()
	if err != nil {
		return nil, err
	}
	return []model.WorkOrder{*wo}, nil
}

func (f *fakeWorkOrders) Update(_ context.Context, id string, p service.WorkOrderPatch) (*model.WorkOrder, error) {
	f.updateID, f.patch = id, p
	return f.result()
}

func (f *fakeWorkOrders) ChangeStatus(_ context.Context, id, to string) (*model.WorkOrder, error) {
	f.statusID, f.statusTo = id, to
	return f.result()
}

func (f *fakeWorkOrders) Delete(_ context.Context, id string) error {
	f.deleteID = id
	_, err := f.result()
	return err
}

func workOrderEngine(f *fakeWorkOrders) *gin.Engine {
	return newEngine(func(r *gin.Engine) {
		h := handler.NewWorkOrderHandler(f)
		r.POST("/jobs/:id/occurrences/:date/work-order", h.Create)
		r.GET("/work-orders", h.List)
		r.GET("/work-orders/:id", h.Get)
		r.PATCH("/work-orders/:id", h.Update)
		r.PATCH("/work-orders/:id/status", h.Status)
		r.DELETE("/work-orders/:id", h.Delete)
	})
}

func TestWorkOrderHandlerCreate(t *testing.T) {
	const path = "/jobs/job-1/occurrences/2026-10-05/work-order"

	t.Run("success envelope with the job, date and input passed down", func(t *testing.T) {
		f := newFakeWorkOrders()
		w := do(workOrderEngine(f), "POST", path, `{"notes":"n","tasks":[{"description":"Clean windows"},{"description":"Mop","done":true}]}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		m := decode(t, w)
		data, _ := m["data"].(map[string]any)
		if m["success"] != true || data["id"] != "wo-1" || data["number"] != "WO-000001" || data["occurrenceDate"] != "2026-10-05" {
			t.Fatalf("body %v", m)
		}
		if f.createJob != "job-1" || !f.createDate.Equal(civil.New(2026, 10, 5)) || f.created.Notes != "n" ||
			len(f.created.Tasks) != 2 || f.created.Tasks[0].Description != "Clean windows" || !f.created.Tasks[1].Done {
			t.Fatalf("job=%q date=%v input=%+v", f.createJob, f.createDate, f.created)
		}
	})

	t.Run("bad requests are 400 and never reach the service", func(t *testing.T) {
		for name, c := range map[string]struct{ path, body string }{
			"bad date in the path": {"/jobs/job-1/occurrences/05-10-2026/work-order", `{}`},
			"task without text":    {path, `{"tasks":[{"done":true}]}`},
			"bad json":             {path, `{`},
		} {
			f := newFakeWorkOrders()
			if w := do(workOrderEngine(f), "POST", c.path, c.body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		for status, err := range map[int]error{404: apperror.NotFound("date is not an occurrence of this job"), 409: apperror.Conflict("a work order already exists for this occurrence")} {
			f := newFakeWorkOrders()
			f.err = err
			if w := do(workOrderEngine(f), "POST", path, `{}`); w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}

func TestWorkOrderHandlerGetAndDelete(t *testing.T) {
	f := newFakeWorkOrders()
	w := do(workOrderEngine(f), "GET", "/work-orders/wo-1", "")
	if w.Code != 200 || f.getID != "wo-1" {
		t.Fatalf("status %d id %q", w.Code, f.getID)
	}
	data, _ := decode(t, w)["data"].(map[string]any)
	tasks, _ := data["tasks"].([]any)
	if len(tasks) != 2 || tasks[1].(map[string]any)["done"] != true {
		t.Fatalf("tasks %v", data["tasks"])
	}

	f = newFakeWorkOrders()
	if w := do(workOrderEngine(f), "DELETE", "/work-orders/wo-1", ""); w.Code != 200 || f.deleteID != "wo-1" {
		t.Fatalf("delete: status %d id %q", w.Code, f.deleteID)
	}

	for status, err := range map[int]error{404: apperror.NotFound("work order not found"), 409: apperror.Conflict("only a draft work order can be deleted")} {
		f = newFakeWorkOrders()
		f.err = err
		if w := do(workOrderEngine(f), "DELETE", "/work-orders/wo-1", ""); w.Code != status {
			t.Errorf("delete: want %d, got %d", status, w.Code)
		}
	}
}

func TestWorkOrderHandlerList(t *testing.T) {
	t.Run("the filters and paging reach the service", func(t *testing.T) {
		f := newFakeWorkOrders()
		w := do(workOrderEngine(f), "GET", "/work-orders?status=scheduled&customerId=c1&locationId=l1&jobId=j1&q=ada&occurrenceFrom=2026-10-01&occurrenceTo=2026-10-31&limit=10&offset=5", "")
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		q := f.listQ
		if q.Status != "scheduled" || q.CustomerID != "c1" || q.LocationID != "l1" || q.JobID != "j1" || q.Q != "ada" || q.Limit != 10 || q.Offset != 5 ||
			!q.OccurrenceFrom.Equal(civil.New(2026, 10, 1)) || !q.OccurrenceTo.Equal(civil.New(2026, 10, 31)) {
			t.Fatalf("query %+v", q)
		}
		if data, _ := decode(t, w)["data"].([]any); len(data) != 1 {
			t.Fatalf("data %v", data)
		}
	})

	t.Run("no filters means a zero query", func(t *testing.T) {
		f := newFakeWorkOrders()
		if w := do(workOrderEngine(f), "GET", "/work-orders", ""); w.Code != 200 {
			t.Fatalf("status %d", w.Code)
		}
		if q := f.listQ; q.Status != "" || q.Limit != 0 || q.Offset != 0 || !q.OccurrenceFrom.IsZero() || !q.OccurrenceTo.IsZero() {
			t.Fatalf("query %+v", q)
		}
	})

	t.Run("bad query values are 400 and never reach the service", func(t *testing.T) {
		for name, qs := range map[string]string{
			"limit not a number":  "limit=x",
			"offset not a number": "offset=x",
			"bad from date":       "occurrenceFrom=10-01-2026",
			"bad to date":         "occurrenceTo=nope",
		} {
			f := newFakeWorkOrders()
			if w := do(workOrderEngine(f), "GET", "/work-orders?"+qs, ""); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		f := newFakeWorkOrders()
		f.err = apperror.Validation("unknown status")
		if w := do(workOrderEngine(f), "GET", "/work-orders?status=paid", ""); w.Code != 400 {
			t.Errorf("status %d", w.Code)
		}
	})
}

func TestWorkOrderHandlerUpdate(t *testing.T) {
	t.Run("notes and a replacement task list reach the service", func(t *testing.T) {
		f := newFakeWorkOrders()
		w := do(workOrderEngine(f), "PATCH", "/work-orders/wo-1", `{"notes":"n","tasks":[{"description":"Clean","done":true}]}`)
		if w.Code != 200 || f.updateID != "wo-1" {
			t.Fatalf("status %d id %q: %s", w.Code, f.updateID, w.Body)
		}
		if f.patch.Notes == nil || *f.patch.Notes != "n" || f.patch.Tasks == nil || len(*f.patch.Tasks) != 1 || !(*f.patch.Tasks)[0].Done {
			t.Fatalf("patch %+v", f.patch)
		}
	})

	t.Run("omitted fields stay nil, an empty list is passed as empty", func(t *testing.T) {
		f := newFakeWorkOrders()
		do(workOrderEngine(f), "PATCH", "/work-orders/wo-1", `{"notes":""}`)
		if f.patch.Tasks != nil || f.patch.Notes == nil {
			t.Fatalf("patch %+v", f.patch)
		}
		f = newFakeWorkOrders()
		do(workOrderEngine(f), "PATCH", "/work-orders/wo-1", `{"tasks":[]}`)
		if f.patch.Notes != nil || f.patch.Tasks == nil || len(*f.patch.Tasks) != 0 {
			t.Fatalf("patch %+v", f.patch)
		}
	})

	t.Run("bad bodies are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{"bad json": `{`, "task without text": `{"tasks":[{"done":true}]}`} {
			f := newFakeWorkOrders()
			if w := do(workOrderEngine(f), "PATCH", "/work-orders/wo-1", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		for status, err := range map[int]error{400: apperror.Validation("nothing to update"), 404: apperror.NotFound("work order not found"), 409: apperror.Conflict("can no longer be edited")} {
			f := newFakeWorkOrders()
			f.err = err
			if w := do(workOrderEngine(f), "PATCH", "/work-orders/wo-1", `{"notes":"n"}`); w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}

func TestWorkOrderHandlerStatus(t *testing.T) {
	t.Run("the id and new status reach the service", func(t *testing.T) {
		f := newFakeWorkOrders()
		w := do(workOrderEngine(f), "PATCH", "/work-orders/wo-1/status", `{"status":"scheduled"}`)
		if w.Code != 200 || f.statusID != "wo-1" || f.statusTo != "scheduled" {
			t.Fatalf("status %d id %q to %q", w.Code, f.statusID, f.statusTo)
		}
	})

	t.Run("a missing status or bad json is 400", func(t *testing.T) {
		for _, body := range []string{`{}`, `{`} {
			f := newFakeWorkOrders()
			if w := do(workOrderEngine(f), "PATCH", "/work-orders/wo-1/status", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", body, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		for status, err := range map[int]error{400: apperror.Validation("add at least one task before completing"), 404: apperror.NotFound("work order not found"), 409: apperror.Conflict("cannot change a work order from draft to completed")} {
			f := newFakeWorkOrders()
			f.err = err
			if w := do(workOrderEngine(f), "PATCH", "/work-orders/wo-1/status", `{"status":"completed"}`); w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}
