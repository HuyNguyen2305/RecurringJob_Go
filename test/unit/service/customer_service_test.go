package service_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// memCustomers is an in-memory service.CustomerStore (and CustomerGetter).
type memCustomers struct {
	rows       map[string]*model.Customer
	seq        int
	createErr  error
	updateErr  error
	listLimit  int
	listOffset int
}

func newMemCustomers() *memCustomers { return &memCustomers{rows: map[string]*model.Customer{}} }

func (m *memCustomers) Create(_ context.Context, c *model.Customer) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.seq++
	c.ID = uid(fmt.Sprintf("%02x", m.seq))
	cp := *c
	m.rows[c.ID] = &cp
	return nil
}

func (m *memCustomers) Get(_ context.Context, id string) (*model.Customer, error) {
	c, ok := m.rows[id]
	if !ok {
		return nil, apperror.NotFound("customer not found")
	}
	cp := *c
	return &cp, nil
}

func (m *memCustomers) List(_ context.Context, limit, offset int) ([]model.Customer, error) {
	m.listLimit, m.listOffset = limit, offset
	out := []model.Customer{}
	for _, c := range m.rows {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *memCustomers) Update(_ context.Context, id string, fields map[string]any) (int64, error) {
	if m.updateErr != nil {
		return 0, m.updateErr
	}
	c, ok := m.rows[id]
	if !ok {
		return 0, nil
	}
	if v, ok := fields["name"]; ok {
		c.Name = v.(string)
	}
	if v, ok := fields["email"]; ok {
		c.Email = v.(*string)
	}
	if v, ok := fields["phone"]; ok {
		c.Phone = v.(*string)
	}
	return 1, nil
}

// memLocations is an in-memory service.LocationStore (and LocationGetter).
type memLocations struct {
	rows      map[string]*model.Location
	seq       int
	createErr error
}

func newMemLocations() *memLocations { return &memLocations{rows: map[string]*model.Location{}} }

func (m *memLocations) Create(_ context.Context, l *model.Location) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.seq++
	l.ID = uid(fmt.Sprintf("%02x", 0x80+m.seq))
	cp := *l
	m.rows[l.ID] = &cp
	return nil
}

func (m *memLocations) Get(_ context.Context, id string) (*model.Location, error) {
	l, ok := m.rows[id]
	if !ok {
		return nil, apperror.NotFound("location not found")
	}
	cp := *l
	return &cp, nil
}

func (m *memLocations) ListByCustomer(_ context.Context, customerID string) ([]model.Location, error) {
	out := []model.Location{}
	for _, l := range m.rows {
		if l.CustomerID == customerID {
			out = append(out, *l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddressLine1 < out[j].AddressLine1 })
	return out, nil
}

func newCustomerService() (*service.CustomerService, *memCustomers, *memLocations) {
	customers, locations := newMemCustomers(), newMemLocations()
	return service.NewCustomerService(customers, locations), customers, locations
}

func strp(s string) *string { return &s }

func TestCreateCustomer(t *testing.T) {
	ctx := context.Background()

	t.Run("a name is enough; email and phone are optional", func(t *testing.T) {
		s, store, _ := newCustomerService()
		c, err := s.CreateCustomer(ctx, service.CustomerInput{Name: "  Ada Lovelace  "})
		if err != nil || c.Name != "Ada Lovelace" || c.Email != nil || c.Phone != nil || c.ID == "" {
			t.Fatalf("c=%+v err=%v", c, err)
		}
		if len(store.rows) != 1 {
			t.Fatal("not saved")
		}
	})

	t.Run("email and phone are trimmed and kept", func(t *testing.T) {
		s, _, _ := newCustomerService()
		c, err := s.CreateCustomer(ctx, service.CustomerInput{Name: "Ada", Email: " ada@example.com ", Phone: " 555-0100 "})
		if err != nil || c.Email == nil || *c.Email != "ada@example.com" || c.Phone == nil || *c.Phone != "555-0100" {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})

	t.Run("blank email and phone are stored as null, not empty strings", func(t *testing.T) {
		s, _, _ := newCustomerService()
		c, err := s.CreateCustomer(ctx, service.CustomerInput{Name: "Ada", Email: "   ", Phone: ""})
		if err != nil || c.Email != nil || c.Phone != nil {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})

	t.Run("rejections save nothing", func(t *testing.T) {
		tests := []struct {
			name string
			in   service.CustomerInput
		}{
			{"no name", service.CustomerInput{}},
			{"blank name", service.CustomerInput{Name: "   "}},
			{"name too long", service.CustomerInput{Name: strings.Repeat("a", service.MaxNameLen+1)}},
			{"email without domain", service.CustomerInput{Name: "A", Email: "ada"}},
			{"email with display name", service.CustomerInput{Name: "A", Email: "Ada <ada@example.com>"}},
			{"email too long", service.CustomerInput{Name: "A", Email: strings.Repeat("a", service.MaxEmailLen) + "@example.com"}},
			{"phone too long", service.CustomerInput{Name: "A", Phone: strings.Repeat("1", service.MaxPhoneLen+1)}},
		}
		for _, tt := range tests {
			s, store, _ := newCustomerService()
			if _, err := s.CreateCustomer(ctx, tt.in); statusOf(t, err) != 400 {
				t.Errorf("%s: %v", tt.name, err)
			}
			if len(store.rows) != 0 {
				t.Errorf("%s: a customer was saved", tt.name)
			}
		}
	})

	t.Run("limits are inclusive", func(t *testing.T) {
		s, _, _ := newCustomerService()
		if _, err := s.CreateCustomer(ctx, service.CustomerInput{Name: strings.Repeat("a", service.MaxNameLen), Phone: strings.Repeat("1", service.MaxPhoneLen)}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a store error is returned as is", func(t *testing.T) {
		s, store, _ := newCustomerService()
		boom := errors.New("db down")
		store.createErr = boom
		if _, err := s.CreateCustomer(ctx, service.CustomerInput{Name: "Ada"}); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestGetAndListCustomers(t *testing.T) {
	ctx := context.Background()
	s, store, _ := newCustomerService()
	for _, n := range []string{"Zed", "Ada", "Mo"} {
		if _, err := s.CreateCustomer(ctx, service.CustomerInput{Name: n}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("get", func(t *testing.T) {
		c, err := s.GetCustomer(ctx, uid("01"))
		if err != nil || c.Name != "Zed" {
			t.Fatalf("c=%+v err=%v", c, err)
		}
		if _, err := s.GetCustomer(ctx, "nope"); statusOf(t, err) != 400 {
			t.Errorf("malformed: %v", err)
		}
		if _, err := s.GetCustomer(ctx, uid("ff")); statusOf(t, err) != 404 {
			t.Errorf("unknown: %v", err)
		}
	})

	t.Run("list uses the default page and passes limit and offset down", func(t *testing.T) {
		if _, err := s.ListCustomers(ctx, 0, 0); err != nil || store.listLimit != service.DefaultCustomerLimit || store.listOffset != 0 {
			t.Fatalf("defaults: limit=%d offset=%d err=%v", store.listLimit, store.listOffset, err)
		}
		if _, err := s.ListCustomers(ctx, 5, 10); err != nil || store.listLimit != 5 || store.listOffset != 10 {
			t.Fatalf("limit=%d offset=%d err=%v", store.listLimit, store.listOffset, err)
		}
		list, err := s.ListCustomers(ctx, service.MaxCustomerLimit, 0)
		if err != nil || len(list) != 3 || list[0].Name != "Ada" {
			t.Fatalf("list=%+v err=%v", list, err)
		}
	})

	t.Run("list rejections", func(t *testing.T) {
		for name, c := range map[string]struct{ limit, offset int }{
			"limit too large": {service.MaxCustomerLimit + 1, 0},
			"negative limit":  {-1, 0},
			"negative offset": {5, -1},
		} {
			if _, err := s.ListCustomers(ctx, c.limit, c.offset); statusOf(t, err) != 400 {
				t.Errorf("%s: %v", name, err)
			}
		}
	})
}

func TestUpdateCustomer(t *testing.T) {
	ctx := context.Background()
	newOne := func() (*service.CustomerService, *memCustomers, string) {
		s, store, _ := newCustomerService()
		c, err := s.CreateCustomer(ctx, service.CustomerInput{Name: "Ada", Email: "ada@example.com", Phone: "555-0100"})
		if err != nil {
			t.Fatal(err)
		}
		return s, store, c.ID
	}

	t.Run("only the given fields change", func(t *testing.T) {
		s, _, id := newOne()
		c, err := s.UpdateCustomer(ctx, id, service.CustomerPatch{Name: strp("  Ada King  ")})
		if err != nil || c.Name != "Ada King" || c.Email == nil || *c.Email != "ada@example.com" || c.Phone == nil || *c.Phone != "555-0100" {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})

	t.Run("an empty email or phone clears it", func(t *testing.T) {
		s, _, id := newOne()
		c, err := s.UpdateCustomer(ctx, id, service.CustomerPatch{Email: strp(""), Phone: strp("  ")})
		if err != nil || c.Email != nil || c.Phone != nil || c.Name != "Ada" {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})

	t.Run("a new email and phone are validated and saved", func(t *testing.T) {
		s, _, id := newOne()
		c, err := s.UpdateCustomer(ctx, id, service.CustomerPatch{Email: strp("new@example.com"), Phone: strp("555-0199")})
		if err != nil || *c.Email != "new@example.com" || *c.Phone != "555-0199" {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})

	t.Run("rejections change nothing", func(t *testing.T) {
		tests := []struct {
			name  string
			id    string
			patch service.CustomerPatch
			want  int
		}{
			{"empty patch", "", service.CustomerPatch{}, 400},
			{"blank name", "", service.CustomerPatch{Name: strp(" ")}, 400},
			{"name too long", "", service.CustomerPatch{Name: strp(strings.Repeat("a", service.MaxNameLen+1))}, 400},
			{"bad email", "", service.CustomerPatch{Email: strp("nope")}, 400},
			{"phone too long", "", service.CustomerPatch{Phone: strp(strings.Repeat("1", service.MaxPhoneLen+1))}, 400},
			{"malformed id", "nope", service.CustomerPatch{Name: strp("x")}, 400},
			{"unknown id", uid("ff"), service.CustomerPatch{Name: strp("x")}, 404},
		}
		for _, tt := range tests {
			s, store, id := newOne()
			if tt.id != "" {
				id = tt.id
			}
			if _, err := s.UpdateCustomer(ctx, id, tt.patch); statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if c := store.rows[uid("01")]; c.Name != "Ada" || *c.Email != "ada@example.com" {
				t.Errorf("%s: the customer changed: %+v", tt.name, c)
			}
		}
	})

	t.Run("a store error is returned as is", func(t *testing.T) {
		s, store, id := newOne()
		boom := errors.New("db down")
		store.updateErr = boom
		if _, err := s.UpdateCustomer(ctx, id, service.CustomerPatch{Name: strp("x")}); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestCreateAndListLocations(t *testing.T) {
	ctx := context.Background()
	newOne := func() (*service.CustomerService, *memLocations, string) {
		s, _, locations := newCustomerService()
		c, _ := s.CreateCustomer(ctx, service.CustomerInput{Name: "Ada"})
		return s, locations, c.ID
	}

	t.Run("a location needs only an address", func(t *testing.T) {
		s, store, cid := newOne()
		l, err := s.CreateLocation(ctx, cid, service.LocationInput{AddressLine1: "  1 Main Street "})
		if err != nil || l.AddressLine1 != "1 Main Street" || l.CustomerID != cid || l.City != nil || l.State != nil || l.Zip != nil {
			t.Fatalf("l=%+v err=%v", l, err)
		}
		if len(store.rows) != 1 {
			t.Fatal("not saved")
		}
	})

	t.Run("city, state and zip are trimmed and kept", func(t *testing.T) {
		s, _, cid := newOne()
		l, err := s.CreateLocation(ctx, cid, service.LocationInput{AddressLine1: "1 Main", City: " Springfield ", State: "IL", Zip: " 62701 "})
		if err != nil || *l.City != "Springfield" || *l.State != "IL" || *l.Zip != "62701" {
			t.Fatalf("l=%+v err=%v", l, err)
		}
	})

	t.Run("rejections save nothing", func(t *testing.T) {
		tests := []struct {
			name string
			cid  string
			in   service.LocationInput
			want int
		}{
			{"no address", "", service.LocationInput{}, 400},
			{"address too long", "", service.LocationInput{AddressLine1: strings.Repeat("a", service.MaxAddressLen+1)}, 400},
			{"city too long", "", service.LocationInput{AddressLine1: "x", City: strings.Repeat("a", service.MaxCityStateLen+1)}, 400},
			{"state too long", "", service.LocationInput{AddressLine1: "x", State: strings.Repeat("a", service.MaxCityStateLen+1)}, 400},
			{"zip too long", "", service.LocationInput{AddressLine1: "x", Zip: strings.Repeat("1", service.MaxZipLen+1)}, 400},
			{"malformed customer id", "nope", service.LocationInput{AddressLine1: "x"}, 400},
			{"unknown customer", uid("ff"), service.LocationInput{AddressLine1: "x"}, 404},
		}
		for _, tt := range tests {
			s, store, cid := newOne()
			if tt.cid != "" {
				cid = tt.cid
			}
			if _, err := s.CreateLocation(ctx, cid, tt.in); statusOf(t, err) != tt.want {
				t.Errorf("%s: %v", tt.name, err)
			}
			if len(store.rows) != 0 {
				t.Errorf("%s: a location was saved", tt.name)
			}
		}
	})

	t.Run("a store error is returned as is", func(t *testing.T) {
		s, store, cid := newOne()
		boom := errors.New("db down")
		store.createErr = boom
		if _, err := s.CreateLocation(ctx, cid, service.LocationInput{AddressLine1: "x"}); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("list returns only that customer's locations", func(t *testing.T) {
		s, _, cid := newOne()
		other, _ := s.CreateCustomer(ctx, service.CustomerInput{Name: "Bob"})
		for _, a := range []string{"B Street", "A Street"} {
			if _, err := s.CreateLocation(ctx, cid, service.LocationInput{AddressLine1: a}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.CreateLocation(ctx, other.ID, service.LocationInput{AddressLine1: "C Street"}); err != nil {
			t.Fatal(err)
		}
		list, err := s.ListLocations(ctx, cid)
		if err != nil || len(list) != 2 || list[0].AddressLine1 != "A Street" {
			t.Fatalf("list=%+v err=%v", list, err)
		}
		empty, err := s.ListLocations(ctx, other.ID)
		if err != nil || len(empty) != 1 {
			t.Fatalf("other=%+v err=%v", empty, err)
		}
	})

	t.Run("listing needs an existing customer", func(t *testing.T) {
		s, _, _ := newOne()
		if _, err := s.ListLocations(ctx, "nope"); statusOf(t, err) != 400 {
			t.Errorf("malformed: %v", err)
		}
		if _, err := s.ListLocations(ctx, uid("ff")); statusOf(t, err) != 404 {
			t.Errorf("unknown: %v", err)
		}
	})
}
