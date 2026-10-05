package service_test

import (
	"context"
	"errors"
	"testing"

	"recurringjob/internal/common/apperror"
	"recurringjob/internal/model"
	"recurringjob/internal/service"
)

// countingCustomers counts lookups so tests can see what Resolve skipped.
type countingCustomers struct {
	*memCustomers
	gets int
	err  error
}

func (c *countingCustomers) Get(ctx context.Context, id string) (*model.Customer, error) {
	c.gets++
	if c.err != nil {
		return nil, c.err
	}
	return c.memCustomers.Get(ctx, id)
}

func TestResolveReferences(t *testing.T) {
	ctx := context.Background()

	// A customer with a location, a second customer with a location, one service type.
	type world struct {
		refs                                *service.References
		customers                           *countingCustomers
		cust, loc, otherCust, otherLoc, typ string
	}
	build := func() world {
		customers := &countingCustomers{memCustomers: newMemCustomers()}
		locations, types := newMemLocations(), newMemServiceTypes()
		cs := service.NewCustomerService(customers.memCustomers, locations)
		a, _ := cs.CreateCustomer(ctx, service.CustomerInput{Name: "Ada"})
		b, _ := cs.CreateCustomer(ctx, service.CustomerInput{Name: "Bob"})
		la, _ := cs.CreateLocation(ctx, a.ID, service.LocationInput{AddressLine1: "1 Main Street"})
		lb, _ := cs.CreateLocation(ctx, b.ID, service.LocationInput{AddressLine1: "2 Side Street"})
		st, _ := service.NewServiceTypeService(types).CreateServiceType(ctx, service.ServiceTypeInput{Name: "Windows"})
		return world{
			refs: service.NewReferences(customers, locations, types), customers: customers,
			cust: a.ID, loc: la.ID, otherCust: b.ID, otherLoc: lb.ID, typ: st.ID,
		}
	}

	t.Run("returns the three records", func(t *testing.T) {
		w := build()
		got, err := w.refs.Resolve(ctx, w.cust, w.loc, w.typ)
		if err != nil || got.Customer.Name != "Ada" || got.Location.AddressLine1 != "1 Main Street" || got.ServiceType.Name != "Windows" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("a malformed id is a 400 and nothing is looked up", func(t *testing.T) {
		w := build()
		for name, ids := range map[string][3]string{
			"customer":     {"nope", w.loc, w.typ},
			"location":     {w.cust, "nope", w.typ},
			"service type": {w.cust, w.loc, "nope"},
			"empty":        {"", "", ""},
		} {
			if _, err := w.refs.Resolve(ctx, ids[0], ids[1], ids[2]); statusOf(t, err) != 400 {
				t.Errorf("%s: %v", name, err)
			}
		}
		if w.customers.gets != 0 {
			t.Fatalf("%d lookups before the ids were checked", w.customers.gets)
		}
	})

	t.Run("a record that does not exist is a 404", func(t *testing.T) {
		w := build()
		for name, ids := range map[string][3]string{
			"customer":     {uid("ff"), w.loc, w.typ},
			"location":     {w.cust, uid("ff"), w.typ},
			"service type": {w.cust, w.loc, uid("ff")},
		} {
			if _, err := w.refs.Resolve(ctx, ids[0], ids[1], ids[2]); statusOf(t, err) != 404 {
				t.Errorf("%s: %v", name, err)
			}
		}
	})

	t.Run("a location of another customer is a 400", func(t *testing.T) {
		w := build()
		_, err := w.refs.Resolve(ctx, w.cust, w.otherLoc, w.typ)
		var ae *apperror.AppError
		if !errors.As(err, &ae) || ae.Status != 400 || ae.Message != "locationId does not belong to the customer" {
			t.Fatalf("got %v", err)
		}
		// The other customer's own location is fine.
		if _, err := w.refs.Resolve(ctx, w.otherCust, w.otherLoc, w.typ); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("an infrastructure error is returned as is", func(t *testing.T) {
		w := build()
		boom := errors.New("db down")
		w.customers.err = boom
		if _, err := w.refs.Resolve(ctx, w.cust, w.loc, w.typ); !errors.Is(err, boom) {
			t.Fatalf("got %v", err)
		}
	})
}
