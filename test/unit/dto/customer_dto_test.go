package dto_test

import (
	"encoding/json"
	"testing"

	"recurringjob/internal/dto"
	"recurringjob/internal/model"
)

func TestCustomerResponses(t *testing.T) {
	t.Run("a customer with only a name has null email and phone", func(t *testing.T) {
		got := dto.NewCustomerResponse(&model.Customer{ID: "c1", Name: "Ada"})
		raw, _ := json.Marshal(got)
		if string(raw) != `{"id":"c1","name":"Ada","email":null,"phone":null}` {
			t.Fatalf("json %s", raw)
		}
	})
	t.Run("optional fields come through when set", func(t *testing.T) {
		email, phone := "ada@example.com", "555-0100"
		got := dto.NewCustomerResponse(&model.Customer{ID: "c1", Name: "Ada", Email: &email, Phone: &phone})
		if got.Email == nil || *got.Email != email || got.Phone == nil || *got.Phone != phone {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("lists are never nil", func(t *testing.T) {
		if got := dto.NewCustomerResponses(nil); got == nil || len(got) != 0 {
			t.Fatalf("nil list: %#v", got)
		}
		if got := dto.NewCustomerResponses([]model.Customer{{ID: "a"}, {ID: "b"}}); len(got) != 2 || got[1].ID != "b" {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestLocationResponses(t *testing.T) {
	city, zip := "Springfield", "62701"
	got := dto.NewLocationResponse(&model.Location{ID: "l1", CustomerID: "c1", AddressLine1: "1 Main Street", City: &city, Zip: &zip})
	if got.ID != "l1" || got.CustomerID != "c1" || got.AddressLine1 != "1 Main Street" || *got.City != city || *got.Zip != zip || got.State != nil {
		t.Fatalf("got %+v", got)
	}
	if got := dto.NewLocationResponses(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil list: %#v", got)
	}
	if got := dto.NewLocationResponses([]model.Location{{ID: "a"}, {ID: "b"}}); len(got) != 2 || got[0].ID != "a" {
		t.Fatalf("got %+v", got)
	}
}

func TestServiceTypeResponses(t *testing.T) {
	desc := "Inside and out"
	got := dto.NewServiceTypeResponse(&model.ServiceType{ID: "s1", Name: "Window cleaning", Description: &desc})
	if got.ID != "s1" || got.Name != "Window cleaning" || got.Description == nil || *got.Description != desc {
		t.Fatalf("got %+v", got)
	}
	if got := dto.NewServiceTypeResponse(&model.ServiceType{ID: "s2", Name: "x"}); got.Description != nil {
		t.Fatalf("got %+v", got)
	}
	if got := dto.NewServiceTypeResponses(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil list: %#v", got)
	}
}
