package handler_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/civil"
	"recurringjob/internal/handler"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

type fakeOccs struct {
	items     []service.ScheduleItem
	schedErr  error
	query     service.ScheduleQuery
	updDate   time.Time
	updReq    service.UpdateOccurrenceRequest
	updErr    error
	updCalled bool
}

func (f *fakeOccs) Schedule(_ context.Context, _ string, q service.ScheduleQuery) ([]service.ScheduleItem, error) {
	f.query = q
	return f.items, f.schedErr
}

func (f *fakeOccs) UpdateOccurrence(_ context.Context, jobID string, date time.Time, req service.UpdateOccurrenceRequest) (*model.JobOccurrence, error) {
	f.updCalled, f.updDate, f.updReq = true, date, req
	if f.updErr != nil {
		return nil, f.updErr
	}
	return &model.JobOccurrence{JobID: jobID, OccurrenceDate: date, Status: req.Status, RescheduledTo: req.RescheduledTo}, nil
}

func TestOccurrenceHandlerSchedule(t *testing.T) {
	t.Run("returns items and passes the query", func(t *testing.T) {
		f := &fakeOccs{items: []service.ScheduleItem{{Date: "2026-10-02", State: "real", Status: "unconfirmed"}}}
		r := newEngine(func(r *gin.Engine) { r.GET("/jobs/:id/schedule", handler.NewOccurrenceHandler(f).Schedule) })
		w := do(r, "GET", "/jobs/abc/schedule?from=2026-10-01&limit=10", "")
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		item := decode(t, w)["data"].([]any)[0].(map[string]any)
		if item["date"] != "2026-10-02" || item["state"] != "real" || item["rescheduledTo"] != nil {
			t.Fatalf("item %v", item)
		}
		if !f.query.From.Equal(civil.New(2026, 10, 1)) || !f.query.To.IsZero() || f.query.Limit != 10 {
			t.Fatalf("query %+v", f.query)
		}
	})

	t.Run("bad query is 400, service error passes through", func(t *testing.T) {
		r := newEngine(func(r *gin.Engine) { r.GET("/jobs/:id/schedule", handler.NewOccurrenceHandler(&fakeOccs{}).Schedule) })
		if w := do(r, "GET", "/jobs/abc/schedule?to=bad", ""); w.Code != 400 {
			t.Errorf("status %d", w.Code)
		}
		r = newEngine(func(r *gin.Engine) {
			r.GET("/jobs/:id/schedule", handler.NewOccurrenceHandler(&fakeOccs{schedErr: apperror.NotFound("job not found")}).Schedule)
		})
		if w := do(r, "GET", "/jobs/abc/schedule", ""); w.Code != 404 {
			t.Errorf("status %d", w.Code)
		}
	})
}

func TestOccurrenceHandlerUpdate(t *testing.T) {
	route := func(f *fakeOccs) *gin.Engine {
		return newEngine(func(r *gin.Engine) { r.PATCH("/jobs/:id/occurrences/:date", handler.NewOccurrenceHandler(f).Update) })
	}

	t.Run("status change", func(t *testing.T) {
		f := &fakeOccs{}
		w := do(route(f), "PATCH", "/jobs/abc/occurrences/2026-10-02", `{"status":"confirmed"}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		data := decode(t, w)["data"].(map[string]any)
		if data["status"] != "confirmed" || data["date"] != "2026-10-02" || data["jobId"] != "abc" {
			t.Fatalf("data %v", data)
		}
		if !f.updDate.Equal(civil.New(2026, 10, 2)) || f.updReq.RescheduledTo != nil {
			t.Fatalf("service got %v %+v", f.updDate, f.updReq)
		}
	})

	t.Run("reschedule passes the target date", func(t *testing.T) {
		f := &fakeOccs{}
		w := do(route(f), "PATCH", "/jobs/abc/occurrences/2026-10-02", `{"status":"rescheduled","rescheduledTo":"2026-10-12"}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if f.updReq.RescheduledTo == nil || !f.updReq.RescheduledTo.Equal(civil.New(2026, 10, 12)) {
			t.Fatalf("rescheduledTo %v", f.updReq.RescheduledTo)
		}
		if decode(t, w)["data"].(map[string]any)["rescheduledTo"] != "2026-10-12" {
			t.Fatalf("body %s", w.Body)
		}
	})

	t.Run("bad input is 400 and never reaches the service", func(t *testing.T) {
		for name, c := range map[string][2]string{
			"bad path date":     {"/jobs/abc/occurrences/nope", `{"status":"confirmed"}`},
			"missing status":    {"/jobs/abc/occurrences/2026-10-02", `{}`},
			"bad rescheduledTo": {"/jobs/abc/occurrences/2026-10-02", `{"status":"rescheduled","rescheduledTo":"x"}`},
			"malformed JSON":    {"/jobs/abc/occurrences/2026-10-02", `{`},
		} {
			f := &fakeOccs{}
			if w := do(route(f), "PATCH", c[0], c[1]); w.Code != 400 || f.updCalled {
				t.Errorf("%s: status %d, called=%v", name, w.Code, f.updCalled)
			}
		}
	})

	t.Run("service 404 and 409 map through", func(t *testing.T) {
		for status, err := range map[int]error{404: apperror.NotFound("x"), 409: apperror.Conflict("x")} {
			w := do(route(&fakeOccs{updErr: err}), "PATCH", "/jobs/abc/occurrences/2026-10-02", `{"status":"confirmed"}`)
			if w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}

func TestOccurrenceHandlerScheduleQuery(t *testing.T) {
	route := func(f *fakeOccs) *gin.Engine {
		return newEngine(func(r *gin.Engine) { r.GET("/jobs/:id/schedule", handler.NewOccurrenceHandler(f).Schedule) })
	}
	t.Run("malformed parameters are 400 and never reach the service", func(t *testing.T) {
		for _, q := range []string{"from=nope", "to=nope", "limit=abc", "limit=1.5", "from=2026-02-30", "limit=99999999999999999999"} {
			f := &fakeOccs{}
			if w := do(route(f), "GET", "/jobs/abc/schedule?"+q, ""); w.Code != 400 || f.query != (service.ScheduleQuery{}) {
				t.Errorf("%s: status %d query %+v", q, w.Code, f.query)
			}
		}
	})
	t.Run("all parameters are passed through", func(t *testing.T) {
		f := &fakeOccs{}
		do(route(f), "GET", "/jobs/abc/schedule?from=2026-10-01&to=2026-12-31&limit=7", "")
		want := service.ScheduleQuery{From: civil.New(2026, 10, 1), To: civil.New(2026, 12, 31), Limit: 7}
		if f.query != want {
			t.Fatalf("query %+v, want %+v", f.query, want)
		}
	})
	t.Run("an empty schedule is an empty list, not null", func(t *testing.T) {
		f := &fakeOccs{items: []service.ScheduleItem{}}
		w := do(route(f), "GET", "/jobs/abc/schedule", "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":[]`) {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
	})
}

func TestOccurrenceHandlerUpdateInputs(t *testing.T) {
	route := func(f *fakeOccs) *gin.Engine {
		return newEngine(func(r *gin.Engine) { r.PATCH("/jobs/:id/occurrences/:date", handler.NewOccurrenceHandler(f).Update) })
	}
	t.Run("bad path dates are 400", func(t *testing.T) {
		for _, d := range []string{"2026-02-30", "2026-1-2", "2026-10-02%20", "20261002", "today", "2026-10-02T00:00:00Z"} {
			f := &fakeOccs{}
			if w := do(route(f), "PATCH", "/jobs/abc/occurrences/"+d, `{"status":"confirmed"}`); w.Code != 400 || f.updCalled {
				t.Errorf("%s: status %d called=%v", d, w.Code, f.updCalled)
			}
		}
	})
	t.Run("bad bodies are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{
			"empty":              ``,
			"null":               `null`,
			"array":              `[]`,
			"empty status":       `{"status":""}`,
			"null status":        `{"status":null}`,
			"numeric status":     `{"status":1}`,
			"numeric target":     `{"status":"rescheduled","rescheduledTo":5}`,
			"empty target":       `{"status":"rescheduled","rescheduledTo":""}`,
			"impossible target":  `{"status":"rescheduled","rescheduledTo":"2026-02-30"}`,
			"target with a time": `{"status":"rescheduled","rescheduledTo":"2026-10-12T00:00:00Z"}`,
		} {
			f := &fakeOccs{}
			if w := do(route(f), "PATCH", "/jobs/abc/occurrences/2026-10-02", body); w.Code != 400 || f.updCalled {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.updCalled)
			}
		}
	})
	t.Run("an explicit null target is treated as absent; unknown fields are ignored", func(t *testing.T) {
		f := &fakeOccs{}
		w := do(route(f), "PATCH", "/jobs/abc/occurrences/2026-10-02", `{"status":"confirmed","rescheduledTo":null,"junk":true}`)
		if w.Code != 200 || f.updReq.RescheduledTo != nil || f.updReq.Status != "confirmed" {
			t.Fatalf("status %d req %+v", w.Code, f.updReq)
		}
	})
	t.Run("the job id path parameter is passed through unchanged", func(t *testing.T) {
		f := &fakeOccs{}
		w := do(route(f), "PATCH", "/jobs/4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2/occurrences/2026-10-02", `{"status":"confirmed"}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"jobId":"4aa72fb5-dbfd-41f1-91d9-7b2e6fabf0c2"`) {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
	})
}

func TestOccurrenceHandlerDoesNotLeakInternalErrors(t *testing.T) {
	secret := errors.New(`dial tcp 10.0.0.5:5432: connect: connection refused`)
	f := &fakeOccs{schedErr: secret, updErr: secret}
	r := newEngine(func(r *gin.Engine) {
		h := handler.NewOccurrenceHandler(f)
		r.GET("/jobs/:id/schedule", h.Schedule)
		r.PATCH("/jobs/:id/occurrences/:date", h.Update)
	})
	for _, w := range []*httptest.ResponseRecorder{
		do(r, "GET", "/jobs/abc/schedule", ""),
		do(r, "PATCH", "/jobs/abc/occurrences/2026-10-02", `{"status":"confirmed"}`),
	} {
		body := w.Body.String()
		if w.Code != 500 || strings.Contains(body, "10.0.0.5") || strings.Contains(body, "dial") || !strings.Contains(body, "internal server error") {
			t.Fatalf("status %d body %s", w.Code, body)
		}
	}
}
