package handler_test

import (
	"context"
	"testing"

	"github.com/gin-gonic/gin"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/handler"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// fakeCustomers records what the handler passes down.
type fakeCustomers struct {
	err    error
	called bool

	createIn   service.CustomerInput
	getID      string
	listLimit  int
	listOffset int
	updateID   string
	patch      service.CustomerPatch
	locCust    string
	locIn      service.LocationInput
	listLocID  string
}

func (f *fakeCustomers) result() (*model.Customer, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	email := "ada@example.com"
	return &model.Customer{ID: "c-1", Name: "Ada", Email: &email}, nil
}

func (f *fakeCustomers) CreateCustomer(_ context.Context, in service.CustomerInput) (*model.Customer, error) {
	f.createIn = in
	return f.result()
}

func (f *fakeCustomers) GetCustomer(_ context.Context, id string) (*model.Customer, error) {
	f.getID = id
	return f.result()
}

func (f *fakeCustomers) ListCustomers(_ context.Context, limit, offset int) ([]model.Customer, error) {
	f.listLimit, f.listOffset = limit, offset
	c, err := f.result()
	if err != nil {
		return nil, err
	}
	return []model.Customer{*c}, nil
}

func (f *fakeCustomers) UpdateCustomer(_ context.Context, id string, p service.CustomerPatch) (*model.Customer, error) {
	f.updateID, f.patch = id, p
	return f.result()
}

func (f *fakeCustomers) CreateLocation(_ context.Context, customerID string, in service.LocationInput) (*model.Location, error) {
	f.locCust, f.locIn = customerID, in
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	city := "Springfield"
	return &model.Location{ID: "l-1", CustomerID: customerID, AddressLine1: in.AddressLine1, City: &city}, nil
}

func (f *fakeCustomers) ListLocations(_ context.Context, customerID string) ([]model.Location, error) {
	f.listLocID = customerID
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	return []model.Location{{ID: "l-1", CustomerID: customerID, AddressLine1: "1 Main Street"}}, nil
}

func customerEngine(f *fakeCustomers) *gin.Engine {
	return newEngine(func(r *gin.Engine) {
		h := handler.NewCustomerHandler(f)
		r.POST("/customers", h.Create)
		r.GET("/customers", h.List)
		r.GET("/customers/:id", h.Get)
		r.PATCH("/customers/:id", h.Update)
		r.POST("/customers/:id/locations", h.CreateLocation)
		r.GET("/customers/:id/locations", h.ListLocations)
	})
}

func TestCustomerHandlerCreate(t *testing.T) {
	t.Run("success envelope and decoded input", func(t *testing.T) {
		f := &fakeCustomers{}
		w := do(customerEngine(f), "POST", "/customers", `{"name":"Ada","email":"ada@example.com","phone":"555-0100"}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		m := decode(t, w)
		data := m["data"].(map[string]any)
		if m["success"] != true || data["id"] != "c-1" || data["name"] != "Ada" || data["email"] != "ada@example.com" || data["phone"] != nil {
			t.Fatalf("body %v", m)
		}
		if f.createIn != (service.CustomerInput{Name: "Ada", Email: "ada@example.com", Phone: "555-0100"}) {
			t.Fatalf("input %+v", f.createIn)
		}
	})

	t.Run("bad requests are 400 and never reach the service", func(t *testing.T) {
		for name, body := range map[string]string{"no name": `{}`, "bad json": `{`, "name of the wrong type": `{"name":5}`} {
			f := &fakeCustomers{}
			if w := do(customerEngine(f), "POST", "/customers", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", name, w.Code, f.called)
			}
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		f := &fakeCustomers{err: apperror.Validation("email must be a valid email address")}
		if w := do(customerEngine(f), "POST", "/customers", `{"name":"Ada"}`); w.Code != 400 {
			t.Fatalf("status %d", w.Code)
		}
	})
}

func TestCustomerHandlerGetAndList(t *testing.T) {
	f := &fakeCustomers{}
	if w := do(customerEngine(f), "GET", "/customers/c-1", ""); w.Code != 200 || f.getID != "c-1" {
		t.Fatalf("get: status %d id %q", w.Code, f.getID)
	}
	f.err = apperror.NotFound("customer not found")
	if w := do(customerEngine(f), "GET", "/customers/c-1", ""); w.Code != 404 {
		t.Fatalf("get missing: %d", w.Code)
	}

	t.Run("list parses limit and offset", func(t *testing.T) {
		f := &fakeCustomers{}
		w := do(customerEngine(f), "GET", "/customers?limit=5&offset=10", "")
		if w.Code != 200 || f.listLimit != 5 || f.listOffset != 10 {
			t.Fatalf("status %d limit=%d offset=%d", w.Code, f.listLimit, f.listOffset)
		}
		if got := decode(t, w)["data"].([]any); len(got) != 1 {
			t.Fatalf("data %v", got)
		}
		do(customerEngine(f), "GET", "/customers", "")
		if f.listLimit != 0 || f.listOffset != 0 {
			t.Fatalf("absent params must be zero: limit=%d offset=%d", f.listLimit, f.listOffset)
		}
	})

	t.Run("a non-numeric limit or offset is a 400", func(t *testing.T) {
		for _, q := range []string{"limit=x", "offset=y"} {
			f := &fakeCustomers{}
			if w := do(customerEngine(f), "GET", "/customers?"+q, ""); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", q, w.Code, f.called)
			}
		}
	})
}

func TestCustomerHandlerUpdate(t *testing.T) {
	t.Run("passes only the fields that were sent, and an empty string through", func(t *testing.T) {
		f := &fakeCustomers{}
		w := do(customerEngine(f), "PATCH", "/customers/c-1", `{"name":"Ada King","email":""}`)
		if w.Code != 200 || f.updateID != "c-1" {
			t.Fatalf("status %d id %q: %s", w.Code, f.updateID, w.Body)
		}
		p := f.patch
		if p.Name == nil || *p.Name != "Ada King" || p.Email == nil || *p.Email != "" || p.Phone != nil {
			t.Fatalf("patch %+v", p)
		}
	})

	t.Run("a bad body is a 400, service errors map through", func(t *testing.T) {
		f := &fakeCustomers{}
		if w := do(customerEngine(f), "PATCH", "/customers/c-1", `{`); w.Code != 400 || f.called {
			t.Fatalf("bad json: %d called=%v", w.Code, f.called)
		}
		f.err = apperror.NotFound("customer not found")
		if w := do(customerEngine(f), "PATCH", "/customers/c-1", `{"name":"x"}`); w.Code != 404 {
			t.Fatalf("missing: %d", w.Code)
		}
	})
}

func TestCustomerHandlerLocations(t *testing.T) {
	t.Run("create passes the customer id and the address fields", func(t *testing.T) {
		f := &fakeCustomers{}
		w := do(customerEngine(f), "POST", "/customers/c-1/locations", `{"addressLine1":"1 Main Street","city":"Springfield","state":"IL","zip":"62701"}`)
		if w.Code != 200 {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		if f.locCust != "c-1" || f.locIn != (service.LocationInput{AddressLine1: "1 Main Street", City: "Springfield", State: "IL", Zip: "62701"}) {
			t.Fatalf("customer %q input %+v", f.locCust, f.locIn)
		}
		data := decode(t, w)["data"].(map[string]any)
		if data["customerId"] != "c-1" || data["addressLine1"] != "1 Main Street" || data["city"] != "Springfield" || data["state"] != nil {
			t.Fatalf("data %v", data)
		}
	})

	t.Run("create rejects a body without an address", func(t *testing.T) {
		for _, body := range []string{`{}`, `{`, `{"city":"x"}`} {
			f := &fakeCustomers{}
			if w := do(customerEngine(f), "POST", "/customers/c-1/locations", body); w.Code != 400 || f.called {
				t.Errorf("%s: status %d called=%v", body, w.Code, f.called)
			}
		}
	})

	t.Run("list returns the customer's locations", func(t *testing.T) {
		f := &fakeCustomers{}
		w := do(customerEngine(f), "GET", "/customers/c-1/locations", "")
		if w.Code != 200 || f.listLocID != "c-1" {
			t.Fatalf("status %d id %q", w.Code, f.listLocID)
		}
		if got := decode(t, w)["data"].([]any); len(got) != 1 || got[0].(map[string]any)["addressLine1"] != "1 Main Street" {
			t.Fatalf("data %v", got)
		}
	})

	t.Run("service errors map through the middleware", func(t *testing.T) {
		f := &fakeCustomers{err: apperror.NotFound("customer not found")}
		if w := do(customerEngine(f), "POST", "/customers/c-1/locations", `{"addressLine1":"x"}`); w.Code != 404 {
			t.Errorf("create: %d", w.Code)
		}
		if w := do(customerEngine(f), "GET", "/customers/c-1/locations", ""); w.Code != 404 {
			t.Errorf("list: %d", w.Code)
		}
	})
}
