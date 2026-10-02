// Package migrator applies SQL migrations to a tenant schema and drops
// schemas. It backs cmd/migrate and is importable by tests.
package migrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"gorm.io/gorm"
)

var schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ValidSchemaName reports whether name is a safe, unquoted Postgres schema name.
func ValidSchemaName(name string) bool { return schemaName.MatchString(name) }

// Result lists which migration files were applied and which were skipped.
type Result struct {
	Applied []string
	Skipped []string
}

// Apply creates the schema if needed and applies every *.sql file in dir
// that the schema has not recorded yet, all in one transaction. Applied
// files are tracked per schema in schema_migrations, so it is re-runnable.
func Apply(db *gorm.DB, schema, dir string) (Result, error) {
	var res Result
	if !ValidSchemaName(schema) {
		return res, fmt.Errorf("invalid schema name %q", schema)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return res, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`CREATE SCHEMA IF NOT EXISTS ` + schema).Error; err != nil {
			return err
		}
		if err := tx.Exec(`SELECT set_config('search_path', ?, true)`, schema+", public").Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (filename text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`).Error; err != nil {
			return err
		}
		for _, f := range files {
			name := filepath.Base(f)
			var n int64
			if err := tx.Raw(`SELECT count(*) FROM schema_migrations WHERE filename = ?`, name).Scan(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				res.Skipped = append(res.Skipped, name)
				continue
			}
			sql, err := os.ReadFile(f)
			if err != nil {
				return err
			}
			if err := tx.Exec(string(sql)).Error; err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if err := tx.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name).Error; err != nil {
				return err
			}
			res.Applied = append(res.Applied, name)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// DropSchema drops the schema and everything in it (idempotent). It refuses
// invalid names and the public schema, and verifies the schema is gone.
func DropSchema(db *gorm.DB, schema string) error {
	if !ValidSchemaName(schema) {
		return fmt.Errorf("invalid schema name %q", schema)
	}
	if schema == "public" {
		return fmt.Errorf("refusing to drop the public schema")
	}
	if err := db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`).Error; err != nil {
		return err
	}
	var n int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.schemata WHERE schema_name = ?`, schema).Scan(&n).Error; err != nil {
		return err
	}
	if n != 0 {
		return fmt.Errorf("schema %s still exists after drop", schema)
	}
	return nil
}
