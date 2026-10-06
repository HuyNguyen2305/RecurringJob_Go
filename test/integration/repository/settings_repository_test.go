package repository_test

import (
	"testing"

	"gorm.io/gorm"

	"recurringjob/internal/repository"
	"recurringjob/test/helpers"
)

func TestSettingsRepository(t *testing.T) {
	db, ctx := helpers.NewTestDB(t)
	settings := repository.NewSettingsRepository(db)

	t.Run("a new schema starts on UTC", func(t *testing.T) {
		got, err := settings.Get(ctx)
		if err != nil || got.ID != 1 || got.Timezone != "UTC" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("SetTimezone updates the one row", func(t *testing.T) {
		got, err := settings.SetTimezone(ctx, "Pacific/Kiritimati")
		if err != nil || got.Timezone != "Pacific/Kiritimati" {
			t.Fatalf("got %+v err=%v", got, err)
		}
		again, err := settings.Get(ctx)
		if err != nil || again.Timezone != "Pacific/Kiritimati" {
			t.Fatalf("got %+v err=%v", again, err)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM tenant_settings`); n != 1 {
			t.Fatalf("%d rows, want 1", n)
		}
		if n := helpers.Scalar(t, ctx, db, `SELECT count(*) FROM tenant_settings WHERE updated_at > created_at`); n != 1 {
			t.Fatal("updated_at must move on update")
		}
	})

	t.Run("without a row the defaults apply and SetTimezone creates it", func(t *testing.T) {
		db2, ctx2 := helpers.NewTestDB(t)
		if err := helpers.Tx(ctx2, db2, func(tx *gorm.DB) error { return tx.Exec(`DELETE FROM tenant_settings`).Error }); err != nil {
			t.Fatal(err)
		}
		r2 := repository.NewSettingsRepository(db2)
		if got, err := r2.Get(ctx2); err != nil || got.Timezone != "UTC" {
			t.Fatalf("got %+v err=%v", got, err)
		}
		if got, err := r2.SetTimezone(ctx2, "Asia/Kolkata"); err != nil || got.Timezone != "Asia/Kolkata" {
			t.Fatalf("got %+v err=%v", got, err)
		}
	})

	t.Run("settings are per schema", func(t *testing.T) {
		db2, ctx2 := helpers.NewTestDB(t)
		r2 := repository.NewSettingsRepository(db2)
		if got, err := r2.Get(ctx2); err != nil || got.Timezone != "UTC" {
			t.Fatalf("other schema: %+v err=%v", got, err)
		}
	})

	t.Run("the database allows only row 1 and a non-empty zone", func(t *testing.T) {
		second := helpers.Tx(ctx, db, func(tx *gorm.DB) error {
			return tx.Exec(`INSERT INTO tenant_settings (id) VALUES (2)`).Error
		})
		if code := pgCode(second); code != "23514" {
			t.Fatalf("second row: pg code %q (err=%v), want 23514 check_violation", code, second)
		}
		empty := helpers.Tx(ctx, db, func(tx *gorm.DB) error {
			return tx.Exec(`UPDATE tenant_settings SET timezone = ''`).Error
		})
		if code := pgCode(empty); code != "23514" {
			t.Fatalf("empty zone: pg code %q (err=%v), want 23514 check_violation", code, empty)
		}
	})
}
