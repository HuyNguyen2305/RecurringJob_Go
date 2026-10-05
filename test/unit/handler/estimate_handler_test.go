package handler_test

import (
	"testing"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/handler"
)

const (
	cust = "11111111-1111-1111-1111-111111111111"
	loc  = "22222222-2222-2222-2222-222222222222"
	svc  = "33333333-3333-3333-3333-333333333333"

	refsJSON = `"customerId":"` + cust + `","locationId":"` + loc + `","serviceTypeId":"` + svc + `"`
)

func estimateEngine(f *fakeDocs) *gin.Engine {
	return newEngine(func(r *gin.Engine) {
		h := handler.NewEstimateHandler(f)
		r.POST("/estimates", h.Create)
		r.POST("/estimates/:id/approve", h.Approve)
	})
}

func TestEstimateHandlerCreate(t *testing.T) {
	t.Run("success envelope and decoded input", func(t *testing.T) {
		f := newFakeDocs()
		w := do(estimateEngine(f), "POST", "/estimates", `{`+refsJSON+`,"notes":"n","lineItems":[{"description":"Clean","quantity":2,"unitPriceCents":500}]}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		m := decode(t, w)
		data := m["data"].(map[string]any)
		if m["success"] != true || data["id"] != "doc-1" || data["customer"].(map[string]any)["name"] != "Ada" {
			t.Fatalf("body %v", m)
		}
		in := f.estimateIn
		if in.CustomerID != cust || in.LocationID != loc || in.ServiceTypeID != svc || in.Notes != "n" ||
			len(in.LineItems) != 1 || in.LineItems[0].Quantity != 2 || in.LineItems[0].UnitPriceCents != 500 {
			t.Fatalf("input %+v", in)
		}
	})

	t.Run("a draft without line items is accepted", func(t *testing.T) {
		f := newFakeDocs()
		if w := do(estimateEngine(f), "POST", "/estimates", `{`+refsJSON+`}`); w.Code != 200 || len(f.estimateIn.LineItems) != 0 {
			t.Fatalf("status %d items %v", w.Code, f.estimateIn.LineItems)
		}
	})

	t.Run("bad requests are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{
			"missing everything":   `{}`,
			"missing customer":     `{"locationId":"` + loc + `","serviceTypeId":"` + svc + `"}`,
			"missing location":     `{"customerId":"` + cust + `","serviceTypeId":"` + svc + `"}`,
			"missing service type": `{"customerId":"` + cust + `","locationId":"` + loc + `"}`,
			"bad json":             `{`,
			"item no description":  `{` + refsJSON + `,"lineItems":[{"quantity":1}]}`,
			"item no quantity":     `{` + refsJSON + `,"lineItems":[{"description":"x"}]}`,
		} {
			f := newFakeDocs()
			if w := do(estimateEngine(f), "POST", "/estimates", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		for status, err := range map[int]error{
			400: apperror.Validation("locationId does not belong to the customer"),
			404: apperror.NotFound("customer not found"),
		} {
			f := newFakeDocs()
			f.err = err
			if w := do(estimateEngine(f), "POST", "/estimates", `{`+refsJSON+`}`); w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}

func TestEstimateHandlerApprove(t *testing.T) {
	t.Run("passes the date, start time, length and recurrence", func(t *testing.T) {
		f := newFakeDocs()
		w := do(estimateEngine(f), "POST", "/estimates/doc-1/approve", `{"date":"2026-10-12","startTime":"09:30","lengthMinutes":90,"recurrence":{"frequency":"daily"}}`)
		if w.Code != 200 || f.approveID != "doc-1" {
			t.Fatalf("status %d id %q: %s", w.Code, f.approveID, w.Body)
		}
		in := f.approveIn
		if !in.Date.Equal(civil.New(2026, 10, 12)) || in.StartTime != "09:30" || in.LengthMinutes != 90 || in.Recurrence == nil || in.Recurrence.Frequency != "daily" {
			t.Fatalf("input %+v", in)
		}
	})

	t.Run("a one-off job has no recurrence", func(t *testing.T) {
		f := newFakeDocs()
		do(estimateEngine(f), "POST", "/estimates/doc-1/approve", `{"date":"2026-10-12","startTime":"09:00","lengthMinutes":60}`)
		if f.approveIn.Recurrence != nil {
			t.Fatalf("recurrence %+v", f.approveIn.Recurrence)
		}
	})

	t.Run("bad requests are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{
			"missing date":       `{"startTime":"09:00","lengthMinutes":60}`,
			"bad date":           `{"date":"12/10/2026","startTime":"09:00","lengthMinutes":60}`,
			"missing start time": `{"date":"2026-10-12","lengthMinutes":60}`,
			"missing length":     `{"date":"2026-10-12","startTime":"09:00"}`,
			"bad json":           `{`,
		} {
			f := newFakeDocs()
			if w := do(estimateEngine(f), "POST", "/estimates/doc-1/approve", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		for status, err := range map[int]error{404: apperror.NotFound("estimate not found"), 409: apperror.Conflict("already approved")} {
			f := newFakeDocs()
			f.err = err
			if w := do(estimateEngine(f), "POST", "/estimates/doc-1/approve", `{"date":"2026-10-12","startTime":"09:00","lengthMinutes":60}`); w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}

// jobRefs are the fields every job needs besides its date, as JSON members.
const jobRefs = refsJSON + `,"startTime":"09:00","lengthMinutes":60`
