package repository_test

import (
	"testing"

	"gorm.io/gorm"

	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/test/helpers"
)

func TestServiceTypeRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	types := repository.NewServiceTypeRepository(db)

	t.Run("Create fills id and timestamps; the description is optional", func(t *testing.T) {
		withDesc := &model.ServiceType{Name: "Window cleaning", Description: str("Inside and out")}
		bare := &model.ServiceType{Name: "Gutters"}
		for _, s := range []*model.ServiceType{withDesc, bare} {
			if err := types.Create(ctx, s); err != nil {
				t.Fatal(err)
			}
			if !uuidRe.MatchString(s.ID) || s.CreatedAt.IsZero() {
				t.Fatalf("not populated: %+v", s)
			}
		}
		got, err := types.Get(ctx, withDesc.ID)
		if err != nil || got.Name != "Window cleaning" || got.Description == nil || *got.Description != "Inside and out" {
			t.Fatalf("got %+v err=%v", got, err)
		}
		if got, _ := types.Get(ctx, bare.ID); got.Description != nil {
			t.Fatalf("description must be null: %+v", got)
		}
	})

	t.Run("Get of an unknown id is a 404", func(t *testing.T) {
		if _, err := types.Get(ctx, "00000000-0000-0000-0000-0000000000ff"); appStatus(t, err) != 404 {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("List is ordered by name and never nil", func(t *testing.T) {
		db2, ctx2 := helpers.NewTestDB(t)
		t2 := repository.NewServiceTypeRepository(db2)
		empty, err := t2.List(ctx2)
		if err != nil || empty == nil || len(empty) != 0 {
			t.Fatalf("empty list: %#v err=%v", empty, err)
		}
		for _, n := range []string{"Windows", "Awnings", "Gutters"} {
			if err := t2.Create(ctx2, &model.ServiceType{Name: n}); err != nil {
				t.Fatal(err)
			}
		}
		got, err := t2.List(ctx2)
		if err != nil || len(got) != 3 || got[0].Name != "Awnings" || got[2].Name != "Windows" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("the database refuses a service type inserted without a name", func(t *testing.T) {
		err := helpers.Tx(ctx, db, func(tx *gorm.DB) error {
			return tx.Exec(`INSERT INTO service_types (description) VALUES ('x')`).Error
		})
		if code := pgCode(err); code != "23502" {
			t.Fatalf("pg code %q (err=%v), want 23502 not_null_violation", code, err)
		}
	})
}
