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

// stubDocs serves both the estimate and the invoice handler.
type stubDocs struct{}

var stubDoc = &model.CustomerDocument{ID: "d", Status: "draft"}

func (stubDocs) Get(context.Context, string) (*model.CustomerDocument, error) { return stubDoc, nil }

func (stubDocs) List(context.Context, service.DocumentListQuery) ([]model.CustomerDocument, error) {
	return nil, nil
}

func (stubDocs) Update(context.Context, string, service.DocumentPatch) (*model.CustomerDocument, error) {
	return stubDoc, nil
}

func (stubDocs) ChangeStatus(context.Context, string, string) (*model.CustomerDocument, error) {
	return stubDoc, nil
}

func (stubDocs) Create(context.Context, service.EstimateInput) (*model.CustomerDocument, error) {
	return stubDoc, nil
}

func (stubDocs) Approve(context.Context, string, service.ApproveEstimateInput) (*model.CustomerDocument, error) {
	return stubDoc, nil
}

type stubInvoices struct{ stubDocs }

func (stubInvoices) Create(context.Context, string, time.Time, service.DocumentInput) (*model.CustomerDocument, error) {
	return stubDoc, nil
}

func handlers(jobs handler.JobService) router.Handlers {
	return router.Handlers{
		Customers:    handler.NewCustomerHandler(stubCustomers{}),
		ServiceTypes: handler.NewServiceTypeHandler(stubTypes{}),
		Jobs:         handler.NewJobHandler(jobs),
		Occurrences:  handler.NewOccurrenceHandler(stubOccs{}),
		Estimates:    handler.NewEstimateHandler(stubDocs{}),
		Invoices:     handler.NewInvoiceHandler(stubInvoices{}),
	}
}

func buildRouter(jobs handler.JobService) http.Handler {
	return router.New("default_schema", handlers(jobs))
}

func newRouter(jobs *stubJobs) http.Handler { return buildRouter(jobs) }

func TestRoutes(t *testing.T) {
	r := router.New("s", handlers(&stubJobs{}))
	var got []string
	for _, rt := range r.Routes() {
		got = append(got, rt.Method+" "+rt.Path)
	}
	sort.Strings(got)
	want := []string{
		"GET /customers",
		"GET /customers/:id",
		"GET /customers/:id/locations",
		"GET /estimates",
		"GET /estimates/:id",
		"GET /invoices",
		"GET /service-types",
		"GET /invoices/:id",
		"GET /jobs/:id/occurrences",
		"GET /jobs/:id/schedule",
		"PATCH /estimates/:id",
		"PATCH /estimates/:id/status",
		"PATCH /invoices/:id",
		"PATCH /invoices/:id/status",
		"PATCH /jobs/:id/occurrences/:date",
		"PATCH /customers/:id",
		"POST /customers",
		"POST /customers/:id/locations",
		"POST /estimates",
		"POST /estimates/:id/approve",
		"POST /jobs",
		"POST /service-types",
		"POST /jobs/:id/occurrences/:date/invoice",
	}
	sort.Strings(want)
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
			{"POST", "/jobs", jobBody, 200},
			{"GET", "/jobs/abc/schedule", "", 200},
			{"PATCH", "/jobs/abc/occurrences/2026-10-02", `{"status":"confirmed"}`, 200},
			{"POST", "/estimates", estimateBody, 200},
			{"GET", "/estimates", "", 200},
			{"GET", "/estimates/abc", "", 200},
			{"PATCH", "/estimates/abc", `{"notes":"x"}`, 200},
			{"PATCH", "/estimates/abc/status", `{"status":"sent"}`, 200},
			{"POST", "/estimates/abc/approve", `{"date":"2026-10-12","startTime":"09:00","lengthMinutes":60}`, 200},
			{"POST", "/jobs/abc/occurrences/2026-10-02/invoice", `{"notes":"n"}`, 200},
			{"POST", "/customers", `{"name":"Ada"}`, 200},
			{"GET", "/customers", "", 200},
			{"GET", "/customers/abc", "", 200},
			{"PATCH", "/customers/abc", `{"name":"Bob"}`, 200},
			{"POST", "/customers/abc/locations", `{"addressLine1":"1 Main Street"}`, 200},
			{"GET", "/customers/abc/locations", "", 200},
			{"POST", "/service-types", `{"name":"Window cleaning"}`, 200},
			{"GET", "/service-types", "", 200},
			{"GET", "/invoices", "", 200},
			{"GET", "/invoices/abc", "", 200},
			{"PATCH", "/invoices/abc", `{"notes":"x"}`, 200},
			{"PATCH", "/invoices/abc/status", `{"status":"sent"}`, 200},
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
		h := buildRouter(panicJobs{})
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

const (
	refs         = `"customerId":"11111111-1111-1111-1111-111111111111","locationId":"22222222-2222-2222-2222-222222222222","serviceTypeId":"33333333-3333-3333-3333-333333333333"`
	jobBody      = `{` + refs + `,"date":"2026-10-02","startTime":"09:00","lengthMinutes":60}`
	estimateBody = `{` + refs + `}`
)

type stubCustomers struct{}

var (
	stubCustomer = &model.Customer{ID: "c", Name: "Ada"}
	stubLocation = &model.Location{ID: "l", CustomerID: "c", AddressLine1: "1 Main Street"}
)

func (stubCustomers) CreateCustomer(context.Context, service.CustomerInput) (*model.Customer, error) {
	return stubCustomer, nil
}

func (stubCustomers) GetCustomer(context.Context, string) (*model.Customer, error) {
	return stubCustomer, nil
}

func (stubCustomers) ListCustomers(context.Context, int, int) ([]model.Customer, error) {
	return nil, nil
}

func (stubCustomers) UpdateCustomer(context.Context, string, service.CustomerPatch) (*model.Customer, error) {
	return stubCustomer, nil
}

func (stubCustomers) CreateLocation(context.Context, string, service.LocationInput) (*model.Location, error) {
	return stubLocation, nil
}

func (stubCustomers) ListLocations(context.Context, string) ([]model.Location, error) {
	return nil, nil
}

type stubTypes struct{}

func (stubTypes) CreateServiceType(context.Context, service.ServiceTypeInput) (*model.ServiceType, error) {
	return &model.ServiceType{ID: "s", Name: "Window cleaning"}, nil
}

func (stubTypes) ListServiceTypes(context.Context) ([]model.ServiceType, error) { return nil, nil }
