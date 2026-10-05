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

// fakeInvoices adds the invoice-specific Create to fakeDocs.
type fakeInvoices struct{ *fakeDocs }

func (f fakeInvoices) Create(_ context.Context, jobID string, date time.Time, in service.DocumentInput) (*model.CustomerDocument, error) {
	f.invoiceJob, f.invoiceDate, f.created = jobID, date, in
	return f.result()
}

func invoiceEngine(f *fakeDocs) *gin.Engine {
	return newEngine(func(r *gin.Engine) {
		h := handler.NewInvoiceHandler(fakeInvoices{f})
		r.POST("/jobs/:id/occurrences/:date/invoice", h.Create)
		r.GET("/invoices/:id", h.Get)
	})
}

func TestInvoiceHandlerCreate(t *testing.T) {
	const path = "/jobs/job-1/occurrences/2026-10-05/invoice"

	t.Run("success envelope with the job, date and input passed down", func(t *testing.T) {
		f := newFakeDocs()
		w := do(invoiceEngine(f), "POST", path, `{"notes":"n","lineItems":[{"description":"Clean","quantity":1,"unitPriceCents":900}]}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if m := decode(t, w); m["success"] != true || m["data"].(map[string]any)["id"] != "doc-1" {
			t.Fatalf("body %v", m)
		}
		if f.invoiceJob != "job-1" || !f.invoiceDate.Equal(civil.New(2026, 10, 5)) || f.created.Notes != "n" || len(f.created.LineItems) != 1 {
			t.Fatalf("job=%q date=%v input=%+v", f.invoiceJob, f.invoiceDate, f.created)
		}
	})

	t.Run("bad requests are 400 and never reach the service", func(t *testing.T) {
		for name, c := range map[string]struct{ path, body string }{
			"bad date in the path": {"/jobs/job-1/occurrences/05-10-2026/invoice", `{"notes":"n"}`},
			"item no quantity":     {path, `{"lineItems":[{"description":"x"}]}`},
			"bad json":             {path, `{`},
		} {
			f := newFakeDocs()
			if w := do(invoiceEngine(f), "POST", c.path, c.body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		for status, err := range map[int]error{404: apperror.NotFound("date is not an occurrence of this job"), 409: apperror.Conflict("an invoice already exists for this job")} {
			f := newFakeDocs()
			f.err = err
			if w := do(invoiceEngine(f), "POST", path, `{"notes":"n"}`); w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}

func TestInvoiceHandlerSharedEndpoints(t *testing.T) {
	f := newFakeDocs()
	if w := do(invoiceEngine(f), "GET", "/invoices/doc-1", ""); w.Code != 200 || f.getID != "doc-1" {
		t.Fatalf("status %d id %q", w.Code, f.getID)
	}
}
