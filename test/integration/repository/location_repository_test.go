package repository_test

import (
	"testing"

	"gorm.io/gorm"

	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/test/helpers"
)

func TestLocationRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	customers := repository.NewCustomerRepository(db)
	locations := repository.NewLocationRepository(db)

	ada := &model.Customer{Name: "Ada"}
	bob := &model.Customer{Name: "Bob"}
	for _, c := range []*model.Customer{ada, bob} {
		if err := customers.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("Create fills id and timestamps; optional fields round-trip", func(t *testing.T) {
		l := &model.Location{CustomerID: ada.ID, AddressLine1: "1 Main Street", City: str("Springfield"), State: str("IL"), Zip: str("62701")}
		if err := locations.Create(ctx, l); err != nil {
			t.Fatal(err)
		}
		if !uuidRe.MatchString(l.ID) || l.CreatedAt.IsZero() {
			t.Fatalf("not populated: %+v", l)
		}
		got, err := locations.Get(ctx, l.ID)
		if err != nil || got.CustomerID != ada.ID || got.AddressLine1 != "1 Main Street" || *got.City != "Springfield" || *got.State != "IL" || *got.Zip != "62701" {
			t.Fatalf("got %+v err=%v", got, err)
		}
		bare := &model.Location{CustomerID: ada.ID, AddressLine1: "Bare"}
		if err := locations.Create(ctx, bare); err != nil {
			t.Fatal(err)
		}
		if got, _ := locations.Get(ctx, bare.ID); got.City != nil || got.State != nil || got.Zip != nil {
			t.Fatalf("optional fields must be null: %+v", got)
		}
	})

	t.Run("Get of an unknown id is a 404", func(t *testing.T) {
		if _, err := locations.Get(ctx, "00000000-0000-0000-0000-0000000000ff"); appStatus(t, err) != 404 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("a location must belong to an existing customer", func(t *testing.T) {
		err := locations.Create(ctx, &model.Location{CustomerID: "00000000-0000-0000-0000-0000000000ff", AddressLine1: "x"})
		if pgCode(err) != "23503" {
			t.Fatalf("pg code %q (err=%v), want 23503", pgCode(err), err)
		}
	})

	t.Run("ListByCustomer returns only that customer's locations, oldest first", func(t *testing.T) {
		for _, a := range []string{"B Street", "A Street"} {
			if err := locations.Create(ctx, &model.Location{CustomerID: bob.ID, AddressLine1: a}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := locations.ListByCustomer(ctx, bob.ID)
		if err != nil || len(got) != 2 || got[0].AddressLine1 != "B Street" || got[1].AddressLine1 != "A Street" {
			t.Fatalf("got %+v err=%v", got, err)
		}
		none, err := locations.ListByCustomer(ctx, "00000000-0000-0000-0000-0000000000ff")
		if err != nil || none == nil || len(none) != 0 {
			t.Fatalf("unknown customer: %#v err=%v", none, err)
		}
	})

	t.Run("a customer with locations cannot be deleted", func(t *testing.T) {
		err := helpers.Tx(ctx, db, func(tx *gorm.DB) error {
			return tx.Exec(`DELETE FROM customers WHERE id = ?`, ada.ID).Error
		})
		if code := pgCode(err); code != "23001" {
			t.Fatalf("pg code %q (err=%v), want 23001 restrict_violation", code, err)
		}
	})
}
