package repository_test

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"recurringjob/internal/model"
	"recurringjob/internal/repository"
	"recurringjob/test/helpers"
)

func str(s string) *string { return &s }

func TestCustomerRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	customers := repository.NewCustomerRepository(db)

	t.Run("Create fills id and timestamps; optional fields round-trip, including null", func(t *testing.T) {
		full := &model.Customer{Name: "Ada Lovelace", Email: str("ada@example.com"), Phone: str("555-0100")}
		bare := &model.Customer{Name: "Bare"}
		for _, c := range []*model.Customer{full, bare} {
			if err := customers.Create(ctx, c); err != nil {
				t.Fatal(err)
			}
			if !uuidRe.MatchString(c.ID) || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
				t.Fatalf("not populated: %+v", c)
			}
		}
		got, err := customers.Get(ctx, full.ID)
		if err != nil || got.Name != "Ada Lovelace" || got.Email == nil || *got.Email != "ada@example.com" || got.Phone == nil || *got.Phone != "555-0100" {
			t.Fatalf("got %+v err=%v", got, err)
		}
		got, err = customers.Get(ctx, bare.ID)
		if err != nil || got.Email != nil || got.Phone != nil {
			t.Fatalf("optional fields must come back null: %+v err=%v", got, err)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM customers WHERE id = ? AND email IS NULL AND phone IS NULL`, bare.ID); n != 1 {
			t.Fatal("a nil email/phone must be stored as SQL NULL")
		}
	})

	t.Run("Get of an unknown id is a 404; a malformed id is a database error", func(t *testing.T) {
		if _, err := customers.Get(ctx, "00000000-0000-0000-0000-0000000000ff"); appStatus(t, err) != 404 {
			t.Fatalf("got %v", err)
		}
		if _, err := customers.Get(ctx, "nope"); err == nil || pgCode(err) != "22P02" {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("List is ordered by name and pages", func(t *testing.T) {
		db2, ctx2 := helpers.NewTestDB(t)
		c2 := repository.NewCustomerRepository(db2)
		for _, n := range []string{"Mo", "Ada", "Zed", "Bob"} {
			if err := c2.Create(ctx2, &model.Customer{Name: n}); err != nil {
				t.Fatal(err)
			}
		}
		names := func(limit, offset int) string {
			list, err := c2.List(ctx2, limit, offset)
			if err != nil || list == nil {
				t.Fatalf("list=%v err=%v", list, err)
			}
			out := ""
			for _, c := range list {
				out += c.Name + ","
			}
			return out
		}
		if got := names(10, 0); got != "Ada,Bob,Mo,Zed," {
			t.Errorf("all: %q", got)
		}
		if got := names(2, 1); got != "Bob,Mo," {
			t.Errorf("page: %q", got)
		}
		if got := names(10, 10); got != "" {
			t.Errorf("past the end: %q", got)
		}
	})

	t.Run("Update changes the given columns, can clear to null, and reports unknown ids", func(t *testing.T) {
		c := &model.Customer{Name: "Ada", Email: str("ada@example.com"), Phone: str("555")}
		if err := customers.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		n, err := customers.Update(ctx, c.ID, map[string]any{"name": "Ada King", "email": (*string)(nil)})
		if err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		got, _ := customers.Get(ctx, c.ID)
		if got.Name != "Ada King" || got.Email != nil || got.Phone == nil || *got.Phone != "555" || !got.UpdatedAt.After(c.UpdatedAt) {
			t.Fatalf("got %+v (updated %v -> %v)", got, c.UpdatedAt, got.UpdatedAt)
		}
		if n, err := customers.Update(ctx, "00000000-0000-0000-0000-0000000000ff", map[string]any{"name": "x"}); err != nil || n != 0 {
			t.Fatalf("unknown id: n=%d err=%v", n, err)
		}
	})

	t.Run("the database refuses a customer inserted without a name", func(t *testing.T) {
		err := helpers.Tx(ctx, db, func(tx *gorm.DB) error {
			return tx.Exec(`INSERT INTO customers (email) VALUES ('x')`).Error
		})
		if code := pgCode(err); code != "23502" {
			t.Fatalf("pg code %q (err=%v), want 23502 not_null_violation", code, err)
		}
	})
}
