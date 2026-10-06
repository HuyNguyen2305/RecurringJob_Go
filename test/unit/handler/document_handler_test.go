package handler_test

import (
	"context"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/handler"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// fakeDocs records what the handlers pass down and serves both the estimate
// and the invoice handler.
type fakeDocs struct {
	doc *model.CustomerDocument
	err error

	getID       string
	listQ       service.DocumentListQuery
	updateID    string
	patch       service.DocumentPatch
	statusID    string
	statusTo    string
	created     service.DocumentInput
	estimateIn  service.EstimateInput
	approveID   string
	deleteID    string
	reopenID    string
	revisionsID string
	revisions   []model.DocumentRevision
	approveIn   service.ApproveEstimateInput
	invoiceJob  string
	invoiceDate time.Time
	called      bool
}

func newFakeDocs() *fakeDocs {
	return &fakeDocs{doc: &model.CustomerDocument{
		ID: "doc-1", Type: "estimate", Status: "draft", Customer: &model.Customer{ID: "c1", Name: "Ada"},
		LineItems: []model.CustomerLineItem{{Description: "Clean", Quantity: 2, UnitPriceCents: 500}},
	}}
}

func (f *fakeDocs) result() (*model.CustomerDocument, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return f.doc, nil
}

func (f *fakeDocs) Get(_ context.Context, id string) (*model.CustomerDocument, error) {
	f.getID = id
	return f.result()
}

func (f *fakeDocs) List(_ context.Context, q service.DocumentListQuery) ([]model.CustomerDocument, error) {
	f.listQ = q
	d, err := f.result()
	if err != nil {
		return nil, err
	}
	return []model.CustomerDocument{*d}, nil
}

func (f *fakeDocs) Update(_ context.Context, id string, p service.DocumentPatch) (*model.CustomerDocument, error) {
	f.updateID, f.patch = id, p
	return f.result()
}

func (f *fakeDocs) ChangeStatus(_ context.Context, id, to string) (*model.CustomerDocument, error) {
	f.statusID, f.statusTo = id, to
	return f.result()
}

func (f *fakeDocs) Create(_ context.Context, in service.EstimateInput) (*model.CustomerDocument, error) {
	f.estimateIn = in
	return f.result()
}

func (f *fakeDocs) Approve(_ context.Context, id string, in service.ApproveEstimateInput) (*model.CustomerDocument, error) {
	f.approveID, f.approveIn = id, in
	return f.result()
}

// engineFor serves the shared endpoints of an estimate handler under /d.
func engineFor(f *fakeDocs) *gin.Engine {
	return newEngine(func(r *gin.Engine) {
		h := handler.NewEstimateHandler(f)
		r.GET("/d", h.List)
		r.GET("/d/:id", h.Get)
		r.PATCH("/d/:id", h.Update)
		r.PATCH("/d/:id/status", h.Status)
		r.DELETE("/d/:id", h.Delete)
		r.POST("/d/:id/reopen", h.Reopen)
		r.GET("/d/:id/revisions", h.Revisions)
	})
}

func TestDocumentHandlerGet(t *testing.T) {
	f := newFakeDocs()
	w := do(engineFor(f), "GET", "/d/doc-1", "")
	if w.Code != 200 || f.getID != "doc-1" {
		t.Fatalf("status %d id %q: %s", w.Code, f.getID, w.Body)
	}
	m := decode(t, w)
	data := m["data"].(map[string]any)
	if m["success"] != true || data["id"] != "doc-1" || data["totalCents"] != float64(1000) {
		t.Fatalf("body %v", m)
	}
	if items := data["lineItems"].([]any); len(items) != 1 || items[0].(map[string]any)["totalCents"] != float64(1000) {
		t.Fatalf("items %v", items)
	}

	f.err = apperror.NotFound("estimate not found")
	if w := do(engineFor(f), "GET", "/d/doc-1", ""); w.Code != 404 {
		t.Fatalf("not found: %d", w.Code)
	}
}

func TestDocumentHandlerList(t *testing.T) {
	t.Run("parses the query and returns the list", func(t *testing.T) {
		f := newFakeDocs()
		w := do(engineFor(f), "GET", "/d?status=sent&limit=5&offset=10", "")
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if f.listQ.Status != "sent" || f.listQ.Limit != 5 || f.listQ.Offset != 10 {
			t.Fatalf("query %+v", f.listQ)
		}
		if got := decode(t, w)["data"].([]any); len(got) != 1 {
			t.Fatalf("data %v", got)
		}
	})

	t.Run("absent params are zero values", func(t *testing.T) {
		f := newFakeDocs()
		do(engineFor(f), "GET", "/d", "")
		if f.listQ != (service.DocumentListQuery{}) {
			t.Fatalf("query %+v", f.listQ)
		}
	})

	t.Run("a non-numeric limit or offset is a 400 and never reaches the service", func(t *testing.T) {
		for _, q := range []string{"limit=x", "offset=y"} {
			f := newFakeDocs()
			if w := do(engineFor(f), "GET", "/d?"+q, ""); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", q, w.Code, f.called)
			}
		}
	})

	t.Run("a service error maps through the middleware", func(t *testing.T) {
		f := newFakeDocs()
		f.err = apperror.Validation("limit must be between 1 and 200")
		if w := do(engineFor(f), "GET", "/d?limit=999", ""); w.Code != 400 {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestDocumentHandlerUpdate(t *testing.T) {
	t.Run("passes only the fields that were sent", func(t *testing.T) {
		f := newFakeDocs()
		w := do(engineFor(f), "PATCH", "/d/doc-1", `{"notes":"hello","lineItems":[{"description":"A","quantity":2,"unitPriceCents":300}]}`)
		if w.Code != 200 || f.updateID != "doc-1" {
			t.Fatalf("status %d id %q: %s", w.Code, f.updateID, w.Body)
		}
		if f.patch.Notes == nil || *f.patch.Notes != "hello" || f.patch.CustomerID != nil || f.patch.LocationID != nil || f.patch.ServiceTypeID != nil {
			t.Fatalf("patch %+v", f.patch)
		}
		if f.patch.LineItems == nil || len(*f.patch.LineItems) != 1 || (*f.patch.LineItems)[0] != (service.LineItemInput{Description: "A", Quantity: 2, UnitPriceCents: 300}) {
			t.Fatalf("items %+v", f.patch.LineItems)
		}
	})

	t.Run("customer, location and service type ids are passed down when sent", func(t *testing.T) {
		f := newFakeDocs()
		do(engineFor(f), "PATCH", "/d/doc-1", `{"customerId":"`+cust+`","locationId":"`+loc+`","serviceTypeId":"`+svc+`"}`)
		p := f.patch
		if p.CustomerID == nil || *p.CustomerID != cust || p.LocationID == nil || *p.LocationID != loc || p.ServiceTypeID == nil || *p.ServiceTypeID != svc || p.Notes != nil {
			t.Fatalf("patch %+v", p)
		}
	})

	t.Run("an empty lineItems list is passed as an empty, non-nil list", func(t *testing.T) {
		f := newFakeDocs()
		do(engineFor(f), "PATCH", "/d/doc-1", `{"lineItems":[]}`)
		if f.patch.LineItems == nil || len(*f.patch.LineItems) != 0 {
			t.Fatalf("items %+v", f.patch.LineItems)
		}
	})

	t.Run("bad bodies are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{
			"bad json":                 `{`,
			"item without description": `{"lineItems":[{"quantity":1}]}`,
			"item without quantity":    `{"lineItems":[{"description":"x"}]}`,
		} {
			f := newFakeDocs()
			if w := do(engineFor(f), "PATCH", "/d/doc-1", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("a conflict maps through the middleware", func(t *testing.T) {
		f := newFakeDocs()
		f.err = apperror.Conflict("only a draft estimate can be edited")
		if w := do(engineFor(f), "PATCH", "/d/doc-1", `{"notes":"x"}`); w.Code != 409 {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestDocumentHandlerStatus(t *testing.T) {
	f := newFakeDocs()
	if w := do(engineFor(f), "PATCH", "/d/doc-1/status", `{"status":"sent"}`); w.Code != 200 || f.statusID != "doc-1" || f.statusTo != "sent" {
		t.Fatalf("status %d id=%q to=%q", w.Code, f.statusID, f.statusTo)
	}
	for name, body := range map[string]string{"missing status": `{}`, "bad json": `{`} {
		g := newFakeDocs()
		if w := do(engineFor(g), "PATCH", "/d/doc-1/status", body); w.Code != 400 || g.called {
			t.Errorf("%s: status %d called=%v", name, w.Code, g.called)
		}
	}
	f.err = apperror.Conflict("cannot change")
	if w := do(engineFor(f), "PATCH", "/d/doc-1/status", `{"status":"paid"}`); w.Code != 409 {
		t.Fatalf("conflict: %d", w.Code)
	}
}

func (f *fakeDocs) Delete(_ context.Context, id string) error {
	f.called, f.deleteID = true, id
	return f.err
}

func (f *fakeDocs) Reopen(_ context.Context, id string) (*model.CustomerDocument, error) {
	f.reopenID = id
	return f.result()
}

func (f *fakeDocs) Revisions(_ context.Context, id string) ([]model.DocumentRevision, error) {
	f.called, f.revisionsID = true, id
	return f.revisions, f.err
}

func TestDocumentHandlerListFilters(t *testing.T) {
	t.Run("every filter reaches the service", func(t *testing.T) {
		f := newFakeDocs()
		path := "/d?status=sent&customerId=c&locationId=l&jobId=j&q=ada%20l&occurrenceFrom=2026-10-01&occurrenceTo=2026-10-31&limit=5"
		if w := do(engineFor(f), "GET", path, ""); w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		want := service.DocumentListQuery{
			Status: "sent", CustomerID: "c", LocationID: "l", JobID: "j", Q: "ada l", Limit: 5,
			OccurrenceFrom: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), OccurrenceTo: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
		}
		if f.listQ != want {
			t.Fatalf("query %+v, want %+v", f.listQ, want)
		}
	})

	t.Run("a bad date is a 400 and never reaches the service", func(t *testing.T) {
		for _, q := range []string{"occurrenceFrom=2026-13-01", "occurrenceTo=yesterday", "occurrenceFrom=2026-10-1"} {
			f := newFakeDocs()
			if w := do(engineFor(f), "GET", "/d?"+q, ""); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", q, w.Code, f.called)
			}
		}
	})
}

func TestDocumentHandlerDelete(t *testing.T) {
	t.Run("deletes and returns the id", func(t *testing.T) {
		f := newFakeDocs()
		w := do(engineFor(f), "DELETE", "/d/doc-1", "")
		data := decode(t, w)["data"].(map[string]any)
		if w.Code != 200 || f.deleteID != "doc-1" || data["id"] != "doc-1" {
			t.Fatalf("status %d id %q body %s", w.Code, f.deleteID, w.Body)
		}
	})
	t.Run("service errors map through the middleware", func(t *testing.T) {
		for status, err := range map[int]error{409: apperror.Conflict("only a draft"), 404: apperror.NotFound("nope"), 400: apperror.Validation("bad id")} {
			f := newFakeDocs()
			f.err = err
			if w := do(engineFor(f), "DELETE", "/d/doc-1", ""); w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}

func TestEstimateHandlerReopenAndRevisions(t *testing.T) {
	t.Run("reopen returns the estimate", func(t *testing.T) {
		f := newFakeDocs()
		w := do(engineFor(f), "POST", "/d/doc-1/reopen", "")
		if w.Code != 200 || f.reopenID != "doc-1" || decode(t, w)["data"].(map[string]any)["id"] != "doc-1" {
			t.Fatalf("status %d id %q body %s", w.Code, f.reopenID, w.Body)
		}
	})
	t.Run("reopen errors map through the middleware", func(t *testing.T) {
		f := newFakeDocs()
		f.err = apperror.Conflict("the job already has invoices")
		if w := do(engineFor(f), "POST", "/d/doc-1/reopen", ""); w.Code != 409 {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("revisions are listed newest first and never null", func(t *testing.T) {
		f := newFakeDocs()
		w := do(engineFor(f), "GET", "/d/doc-1/revisions", "")
		if got, ok := decode(t, w)["data"].([]any); w.Code != 200 || !ok || len(got) != 0 || f.revisionsID != "doc-1" {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
		f.revisions = []model.DocumentRevision{
			{Revision: 2, Content: model.RevisionContent{Notes: "v2", LineItems: []model.RevisionLine{{Description: "A", Quantity: 2, UnitPriceCents: 300}}}},
			{Revision: 1, Content: model.RevisionContent{Notes: "v1"}},
		}
		got := decode(t, do(engineFor(f), "GET", "/d/doc-1/revisions", ""))["data"].([]any)
		first := got[0].(map[string]any)
		if len(got) != 2 || first["revision"] != float64(2) || first["notes"] != "v2" || first["totalCents"] != float64(600) {
			t.Fatalf("data %v", got)
		}
	})
	t.Run("revisions errors map through the middleware", func(t *testing.T) {
		f := newFakeDocs()
		f.err = apperror.NotFound("estimate not found")
		if w := do(engineFor(f), "GET", "/d/doc-1/revisions", ""); w.Code != 404 {
			t.Fatalf("status %d", w.Code)
		}
	})
}
