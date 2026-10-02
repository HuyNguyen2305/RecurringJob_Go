package router_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/common/auth"
	"recurringjob/internal/handler"
	"recurringjob/internal/model"
	"recurringjob/internal/router"
	"recurringjob/internal/service"
)

type stubJobs struct{ schema string }

func (s *stubJobs) CreateJob(ctx context.Context, in service.CreateJobInput) (*model.Job, error) {
	s.schema = auth.TenantSchemaFromContext(ctx)
	return &model.Job{ID: "j", Date: in.Date}, nil
}

func (s *stubJobs) Occurrences(ctx context.Context, _ string, _, _ time.Time, _ int) ([]string, error) {
	s.schema = auth.TenantSchemaFromContext(ctx)
	return nil, apperror.NotFound("job not found")
}

type stubOccs struct{}

func (stubOccs) Schedule(context.Context, string, service.ScheduleQuery) ([]service.ScheduleItem, error) {
	return []service.ScheduleItem{}, nil
}

func (stubOccs) UpdateOccurrence(_ context.Context, id string, d time.Time, r service.UpdateOccurrenceRequest) (*model.JobOccurrence, error) {
	return &model.JobOccurrence{JobID: id, OccurrenceDate: d, Status: r.Status}, nil
}

func newRouter(jobs *stubJobs) http.Handler {
	return router.New("default_schema", handler.NewJobHandler(jobs), handler.NewOccurrenceHandler(stubOccs{}))
}

func TestRoutes(t *testing.T) {
	r := router.New("s", handler.NewJobHandler(&stubJobs{}), handler.NewOccurrenceHandler(stubOccs{}))
	var got []string
	for _, rt := range r.Routes() {
		got = append(got, rt.Method+" "+rt.Path)
	}
	sort.Strings(got)
	want := []string{
		"GET /jobs/:id/occurrences",
		"GET /jobs/:id/schedule",
		"PATCH /jobs/:id/occurrences/:date",
		"POST /jobs",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("routes\n got  %v\n want %v", got, want)
	}
}

func TestRouterBehaviour(t *testing.T) {
	do := func(h http.Handler, method, path, body, tenant string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if tenant != "" {
			req.Header.Set(auth.TenantHeader, tenant)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	t.Run("routes dispatch to handlers", func(t *testing.T) {
		h := newRouter(&stubJobs{})
		for _, c := range []struct {
			method, path, body string
			want               int
		}{
			{"POST", "/jobs", `{"date":"2026-10-02"}`, 200},
			{"GET", "/jobs/abc/schedule", "", 200},
			{"PATCH", "/jobs/abc/occurrences/2026-10-02", `{"status":"confirmed"}`, 200},
		} {
			if w := do(h, c.method, c.path, c.body, ""); w.Code != c.want {
				t.Errorf("%s %s: %d, want %d (%s)", c.method, c.path, w.Code, c.want, w.Body)
			}
		}
	})

	t.Run("unknown routes and wrong methods are not served", func(t *testing.T) {
		h := newRouter(&stubJobs{})
		for _, c := range []struct{ method, path string }{
			{"GET", "/nope"}, {"DELETE", "/jobs/abc/occurrences/2026-10-02"}, {"PUT", "/jobs"}, {"GET", "/jobs"},
		} {
			if w := do(h, c.method, c.path, "", ""); w.Code != 404 && w.Code != 405 {
				t.Errorf("%s %s: %d", c.method, c.path, w.Code)
			}
		}
	})

	t.Run("error middleware maps service errors", func(t *testing.T) {
		if w := do(newRouter(&stubJobs{}), "GET", "/jobs/abc/occurrences", "", ""); w.Code != 404 || !strings.Contains(w.Body.String(), `"success":false`) {
			t.Fatalf("status %d body %s", w.Code, w.Body)
		}
	})

	t.Run("tenant schema: header wins, default otherwise", func(t *testing.T) {
		jobs := &stubJobs{}
		h := newRouter(jobs)
		do(h, "GET", "/jobs/abc/occurrences", "", "tenant_x")
		if jobs.schema != "tenant_x" {
			t.Fatalf("schema %q", jobs.schema)
		}
		do(h, "GET", "/jobs/abc/occurrences", "", "")
		if jobs.schema != "default_schema" {
			t.Fatalf("schema %q", jobs.schema)
		}
	})

	t.Run("a panicking handler becomes a 500, not a crash", func(t *testing.T) {
		h := router.New("s", handler.NewJobHandler(panicJobs{}), handler.NewOccurrenceHandler(stubOccs{}))
		if w := do(h, "GET", "/jobs/abc/occurrences", "", ""); w.Code != 500 {
			t.Fatalf("status %d", w.Code)
		}
	})
}

type panicJobs struct{}

func (panicJobs) CreateJob(context.Context, service.CreateJobInput) (*model.Job, error) {
	panic("boom")
}

func (panicJobs) Occurrences(context.Context, string, time.Time, time.Time, int) ([]string, error) {
	panic("boom")
}
