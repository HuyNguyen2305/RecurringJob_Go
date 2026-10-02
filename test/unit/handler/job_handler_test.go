package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

type fakeJobs struct {
	createIn  service.CreateJobInput
	createErr error
	occDates  []string
	occErr    error
	occFrom   time.Time
	occTo     time.Time
	occLimit  int
}

func (f *fakeJobs) CreateJob(_ context.Context, in service.CreateJobInput) (*model.Job, error) {
	f.createIn = in
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &model.Job{ID: "job-1", Date: in.Date, Status: "unconfirmed", Recurrence: in.Recurrence}, nil
}

func (f *fakeJobs) Occurrences(_ context.Context, _ string, from, to time.Time, limit int) ([]string, error) {
	f.occFrom, f.occTo, f.occLimit = from, to, limit
	return f.occDates, f.occErr
}

func newEngine(register func(r *gin.Engine)) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(apperror.ErrorHandler())
	register(r)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("bad JSON %q: %v", w.Body.String(), err)
	}
	return m
}

func TestJobHandlerCreate(t *testing.T) {
	t.Run("success envelope with recurrence passed through", func(t *testing.T) {
		f := &fakeJobs{}
		r := newEngine(func(r *gin.Engine) { r.POST("/jobs", handler.NewJobHandler(f).Create) })
		w := do(r, "POST", "/jobs", `{"date":"2026-10-02","recurrence":{"frequency":"weekly","weeklyPeriod":"every","weeklyDaysOfWeek":[1,3]}}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		m := decode(t, w)
		data := m["data"].(map[string]any)
		if m["success"] != true || data["id"] != "job-1" || data["date"] != "2026-10-02" {
			t.Fatalf("body %v", m)
		}
		if f.createIn.Recurrence == nil || f.createIn.Recurrence.Frequency != "weekly" || len(f.createIn.Recurrence.WeeklyDaysOfWeek) != 2 {
			t.Fatalf("recurrence not decoded: %+v", f.createIn.Recurrence)
		}
		if !f.createIn.Date.Equal(civil.New(2026, 10, 2)) {
			t.Fatalf("date %v", f.createIn.Date)
		}
	})

	t.Run("bad requests are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{
			"missing date": `{}`,
			"bad date":     `{"date":"10/02/2026"}`,
			"bad json":     `{`,
		} {
			f := &fakeJobs{}
			r := newEngine(func(r *gin.Engine) { r.POST("/jobs", handler.NewJobHandler(f).Create) })
			w := do(r, "POST", "/jobs", body)
			if w.Code != 400 || decode(t, w)["success"] != false {
				t.Errorf("%s: status %d body %s", name, w.Code, w.Body)
			}
			if !f.createIn.Date.IsZero() {
				t.Errorf("%s: service was called", name)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		for status, err := range map[int]error{400: apperror.Validation("v"), 404: apperror.NotFound("n"), 500: context.DeadlineExceeded} {
			f := &fakeJobs{createErr: err}
			r := newEngine(func(r *gin.Engine) { r.POST("/jobs", handler.NewJobHandler(f).Create) })
			if w := do(r, "POST", "/jobs", `{"date":"2026-10-02"}`); w.Code != status {
				t.Errorf("want %d, got %d", status, w.Code)
			}
		}
	})
}

func TestJobHandlerOccurrences(t *testing.T) {
	t.Run("parses query and returns dates", func(t *testing.T) {
		f := &fakeJobs{occDates: []string{"2026-10-02", "2026-10-03"}}
		r := newEngine(func(r *gin.Engine) { r.GET("/jobs/:id/occurrences", handler.NewJobHandler(f).Occurrences) })
		w := do(r, "GET", "/jobs/abc/occurrences?from=2026-10-01&to=2026-10-09&limit=5", "")
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if got := decode(t, w)["data"].([]any); len(got) != 2 || got[0] != "2026-10-02" {
			t.Fatalf("data %v", got)
		}
		if !f.occFrom.Equal(civil.New(2026, 10, 1)) || !f.occTo.Equal(civil.New(2026, 10, 9)) || f.occLimit != 5 {
			t.Fatalf("query: %v %v %d", f.occFrom, f.occTo, f.occLimit)
		}
	})

	t.Run("absent params are zero values", func(t *testing.T) {
		f := &fakeJobs{}
		r := newEngine(func(r *gin.Engine) { r.GET("/jobs/:id/occurrences", handler.NewJobHandler(f).Occurrences) })
		do(r, "GET", "/jobs/abc/occurrences", "")
		if !f.occFrom.IsZero() || !f.occTo.IsZero() || f.occLimit != 0 {
			t.Fatalf("query: %v %v %d", f.occFrom, f.occTo, f.occLimit)
		}
	})

	t.Run("malformed query is 400", func(t *testing.T) {
		for _, q := range []string{"from=nope", "to=2026-13-40", "limit=abc"} {
			r := newEngine(func(r *gin.Engine) { r.GET("/jobs/:id/occurrences", handler.NewJobHandler(&fakeJobs{}).Occurrences) })
			if w := do(r, "GET", "/jobs/abc/occurrences?"+q, ""); w.Code != 400 {
				t.Errorf("%s: status %d", q, w.Code)
			}
		}
	})

	t.Run("not found maps to 404", func(t *testing.T) {
		f := &fakeJobs{occErr: apperror.NotFound("job not found")}
		r := newEngine(func(r *gin.Engine) { r.GET("/jobs/:id/occurrences", handler.NewJobHandler(f).Occurrences) })
		if w := do(r, "GET", "/jobs/zzz/occurrences", ""); w.Code != 404 {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestJobHandlerLimitParsing(t *testing.T) {
	tests := []struct {
		query     string
		wantCode  int
		wantLimit int
	}{
		{"", 200, 0},
		{"limit=", 200, 0},
		{"limit=0", 200, 0},
		{"limit=5", 200, 5},
		{"limit=%2B5", 200, 5}, // an encoded plus sign
		{"limit=+5", 400, 0},   // a raw '+' in a query string is a space
		{"limit=-1", 200, -1},  // range checking belongs to the service
		{"limit=1000", 200, 1000},
		{"limit=abc", 400, 0},
		{"limit=1e3", 400, 0},
		{"limit=1.5", 400, 0},
		{"limit=%205", 400, 0},
		{"limit=99999999999999999999", 400, 0},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			f := &fakeJobs{}
			r := newEngine(func(r *gin.Engine) { r.GET("/jobs/:id/occurrences", handler.NewJobHandler(f).Occurrences) })
			w := do(r, "GET", "/jobs/abc/occurrences?"+tt.query, "")
			if w.Code != tt.wantCode || (tt.wantCode == 200 && f.occLimit != tt.wantLimit) {
				t.Fatalf("status %d limit %d, want %d and %d", w.Code, f.occLimit, tt.wantCode, tt.wantLimit)
			}
		})
	}
}

func TestJobHandlerCreateBodies(t *testing.T) {
	serve := func(f *fakeJobs, req *http.Request) *httptest.ResponseRecorder {
		r := newEngine(func(r *gin.Engine) { r.POST("/jobs", handler.NewJobHandler(f).Create) })
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("rejected bodies never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{
			"empty body":                   ``,
			"null":                         `null`,
			"array":                        `[]`,
			"number date":                  `{"date":20261002}`,
			"empty date":                   `{"date":""}`,
			"date with whitespace":         `{"date":" 2026-10-02"}`,
			"impossible date":              `{"date":"2026-02-30"}`,
			"date with time":               `{"date":"2026-10-02T00:00:00Z"}`,
			"status of the wrong type":     `{"date":"2026-10-02","status":5}`,
			"recurrence of the wrong type": `{"date":"2026-10-02","recurrence":"daily"}`,
			"weekday list of strings":      `{"date":"2026-10-02","recurrence":{"frequency":"weekly","weeklyDaysOfWeek":["mon"]}}`,
			"interval of the wrong type":   `{"date":"2026-10-02","recurrence":{"frequency":"daily","interval":"2"}}`,
		} {
			f := &fakeJobs{}
			w := serve(f, httptest.NewRequest("POST", "/jobs", strings.NewReader(body)))
			if w.Code != 400 || !f.createIn.Date.IsZero() {
				t.Errorf("%s: status %d, service called=%v", name, w.Code, !f.createIn.Date.IsZero())
			}
		}
	})

	t.Run("works without a Content-Type header", func(t *testing.T) {
		f := &fakeJobs{}
		w := serve(f, httptest.NewRequest("POST", "/jobs", strings.NewReader(`{"date":"2026-10-02"}`)))
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
	})

	t.Run("unknown JSON fields are ignored and status is passed through", func(t *testing.T) {
		f := &fakeJobs{}
		w := serve(f, httptest.NewRequest("POST", "/jobs", strings.NewReader(`{"date":"2026-10-02","status":"confirmed","extra":{"a":1}}`)))
		if w.Code != 200 || f.createIn.Status != "confirmed" {
			t.Fatalf("status %d createIn %+v", w.Code, f.createIn)
		}
	})

	t.Run("a one-off job passes a nil recurrence", func(t *testing.T) {
		f := &fakeJobs{}
		serve(f, httptest.NewRequest("POST", "/jobs", strings.NewReader(`{"date":"2026-10-02","recurrence":null}`)))
		if f.createIn.Recurrence != nil {
			t.Fatalf("recurrence %+v", f.createIn.Recurrence)
		}
	})
}

func TestJobHandlerDoesNotLeakInternalErrors(t *testing.T) {
	secret := errors.New(`pq: password authentication failed for user "postgres"`)
	f := &fakeJobs{createErr: secret, occErr: secret}
	r := newEngine(func(r *gin.Engine) {
		h := handler.NewJobHandler(f)
		r.POST("/jobs", h.Create)
		r.GET("/jobs/:id/occurrences", h.Occurrences)
	})
	for _, w := range []*httptest.ResponseRecorder{
		do(r, "POST", "/jobs", `{"date":"2026-10-02"}`),
		do(r, "GET", "/jobs/abc/occurrences", ""),
	} {
		body := w.Body.String()
		if w.Code != 500 || strings.Contains(body, "pq") || strings.Contains(body, "password") || !strings.Contains(body, "internal server error") {
			t.Fatalf("status %d body %s", w.Code, body)
		}
	}
}
