package migrator_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"

	"recurringjob/internal/migrator"
	"recurringjob/test/helpers"
)

// realMigrations are the files in the repository's migrations folder, in order.
var realMigrations = []string{"0001_reference_data.sql", "0002_jobs_and_occurrences.sql", "0003_customer_documents.sql", "0004_tenant_settings.sql", "0005_document_lifecycle.sql", "0006_work_orders.sql", "0007_remove_in_progress.sql"}

func TestMain(m *testing.M) {
	helpers.ApplyLocalTZ()
	os.Exit(m.Run())
}

func TestValidSchemaName(t *testing.T) {
	valid := []string{"public", "tenant_1", "_x", "a", "t_8cfd917d5386", strings.Repeat("a", 63)}
	invalid := []string{"", "Bad", "UPPER", "bad-name", "1abc", "has space", "a;drop schema public", `a"b`, "é", strings.Repeat("a", 64), "a.b", "pg catalog"}
	for _, s := range valid {
		if !migrator.ValidSchemaName(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range invalid {
		if migrator.ValidSchemaName(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func schemaExists(t *testing.T, db *gorm.DB, name string) bool {
	t.Helper()
	var n int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.schemata WHERE schema_name = ?`, name).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func tableCount(t *testing.T, db *gorm.DB, schema string, names ...string) int64 {
	t.Helper()
	var n int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_schema = ? AND table_name IN ?`, schema, names).Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func newName(t *testing.T, db *gorm.DB) string {
	t.Helper()
	name := helpers.RandomSchemaName("mig_")
	t.Cleanup(func() { _ = migrator.DropSchema(db, name) })
	return name
}

func writeSQL(t *testing.T, dir, name, sql string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestApplyRealMigrations(t *testing.T) {
	db := helpers.Connect(t)
	schema := newName(t, db)

	res, err := migrator.Apply(db, schema, helpers.MigrationsDir())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Applied, realMigrations) || len(res.Skipped) != 0 {
		t.Fatalf("result %+v", res)
	}
	if n := tableCount(t, db, schema, "customers", "locations", "service_types", "jobs", "job_occurrences", "customer_documents", "customer_line_items", "schema_migrations"); n != 8 {
		t.Fatalf("%d of the 8 tables exist", n)
	}

	t.Run("the schema has the constraints the business rules rely on", func(t *testing.T) {
		var names []string
		err := db.Raw(`SELECT c.conname FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = ? ORDER BY c.conname`, schema).Scan(&names).Error
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(names, ",")
		if !strings.Contains(joined, "job_occurrences_job_date_key") {
			t.Errorf("missing the unique (job_id, occurrence_date) constraint: %s", joined)
		}
		count := func(kind string) int64 {
			var n int64
			db.Raw(`SELECT count(*) FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = ? AND c.contype = ?`, schema, kind).Scan(&n)
			return n
		}
		if count("f") < 1 {
			t.Error("missing the job_occurrences -> jobs foreign key")
		}
		if count("c") < 2 {
			t.Error("missing the status CHECK constraints")
		}
		deleteRule := func(table string) string {
			var rule string
			db.Raw(`SELECT rc.delete_rule FROM information_schema.referential_constraints rc
				JOIN information_schema.table_constraints tc ON tc.constraint_schema = rc.constraint_schema AND tc.constraint_name = rc.constraint_name
				WHERE rc.constraint_schema = ? AND tc.table_name = ?`, schema, table).Scan(&rule)
			return rule
		}
		if got := deleteRule("job_occurrences"); got != "CASCADE" {
			t.Errorf("job_occurrences delete rule %q, want CASCADE", got)
		}
		if got := deleteRule("customer_line_items"); got != "CASCADE" {
			t.Errorf("customer_line_items delete rule %q, want CASCADE", got)
		}
		if got := deleteRule("customer_documents"); got == "CASCADE" {
			t.Error("deleting a job must not silently delete its estimates and invoices")
		}
		// A customer, location or service type that is in use cannot be deleted.
		for table, want := range map[string]int{"locations": 1, "jobs": 3, "customer_documents": 4} {
			var rules []string
			db.Raw(`SELECT rc.delete_rule FROM information_schema.referential_constraints rc
				JOIN information_schema.table_constraints tc ON tc.constraint_schema = rc.constraint_schema AND tc.constraint_name = rc.constraint_name
				WHERE rc.constraint_schema = ? AND tc.table_name = ?`, schema, table).Scan(&rules)
			if len(rules) != want {
				t.Errorf("%s has %d foreign keys, want %d", table, len(rules), want)
			}
			for _, r := range rules {
				if r != "RESTRICT" {
					t.Errorf("%s has a foreign key with delete rule %s, want RESTRICT", table, r)
				}
			}
		}
	})

	t.Run("running it again skips everything and changes nothing", func(t *testing.T) {
		res, err := migrator.Apply(db, schema, helpers.MigrationsDir())
		if err != nil || len(res.Applied) != 0 || !reflect.DeepEqual(res.Skipped, realMigrations) {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})
}

// copyMigration copies one real migration file into dir.
func copyMigration(t *testing.T, dir, name string) {
	t.Helper()
	sql, err := os.ReadFile(filepath.Join(helpers.MigrationsDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	writeSQL(t, dir, name, string(sql))
}

func TestRemoveInProgressMigration(t *testing.T) {
	db := helpers.Connect(t)
	schema := newName(t, db)
	dir := t.TempDir()
	for _, name := range realMigrations[:len(realMigrations)-1] {
		copyMigration(t, dir, name)
	}
	if _, err := migrator.Apply(db, schema, dir); err != nil {
		t.Fatal(err)
	}

	// A job and a stored occurrence that are still in_progress before the migration.
	q := func(format string) string { return strings.ReplaceAll(format, "S.", schema+".") }
	for _, stmt := range []string{
		`INSERT INTO S.customers (id, name) VALUES ('00000000-0000-0000-0000-0000000000c1', 'Ada')`,
		`INSERT INTO S.locations (id, customer_id, address_line1) VALUES ('00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000c1', '1 Main Street')`,
		`INSERT INTO S.service_types (id, name) VALUES ('00000000-0000-0000-0000-0000000000b1', 'Window cleaning')`,
		`INSERT INTO S.jobs (id, customer_id, location_id, service_type_id, date, start_time, length_minutes, status)
			VALUES ('00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000b1', '2026-10-01', '09:00', 60, 'in_progress')`,
		`INSERT INTO S.job_occurrences (job_id, occurrence_date, status) VALUES ('00000000-0000-0000-0000-0000000000e1', '2026-10-02', 'in_progress')`,
		`INSERT INTO S.job_occurrences (job_id, occurrence_date, status) VALUES ('00000000-0000-0000-0000-0000000000e1', '2026-10-03', 'canceled')`,
	} {
		if err := db.Exec(q(stmt)).Error; err != nil {
			t.Fatal(err)
		}
	}

	copyMigration(t, dir, realMigrations[len(realMigrations)-1])
	res, err := migrator.Apply(db, schema, dir)
	if err != nil || !reflect.DeepEqual(res.Applied, []string{"0007_remove_in_progress.sql"}) {
		t.Fatalf("res=%+v err=%v", res, err)
	}

	var jobStatus string
	db.Raw(q(`SELECT status FROM S.jobs`)).Scan(&jobStatus)
	var occStatuses []string
	db.Raw(q(`SELECT status FROM S.job_occurrences ORDER BY occurrence_date`)).Scan(&occStatuses)
	if jobStatus != "confirmed" || !reflect.DeepEqual(occStatuses, []string{"confirmed", "canceled"}) {
		t.Fatalf("job %q occurrences %v: in_progress rows must become confirmed and others stay", jobStatus, occStatuses)
	}

	// The constraints now refuse in_progress on both tables.
	if err := db.Exec(q(`UPDATE S.jobs SET status = 'in_progress'`)).Error; err == nil {
		t.Error("jobs still accept in_progress")
	}
	if err := db.Exec(q(`UPDATE S.job_occurrences SET status = 'in_progress' WHERE status = 'confirmed'`)).Error; err == nil {
		t.Error("job_occurrences still accept in_progress")
	}
	// Rescheduled stays occurrence-only.
	if err := db.Exec(q(`UPDATE S.jobs SET status = 'rescheduled'`)).Error; err == nil {
		t.Error("jobs accept rescheduled")
	}
}

func TestApplyOrderingAndIncrementalFiles(t *testing.T) {
	db := helpers.Connect(t)
	schema := newName(t, db)
	dir := t.TempDir()
	writeSQL(t, dir, "0002_b.sql", `CREATE TABLE b (id int);`)
	writeSQL(t, dir, "0001_a.sql", `CREATE TABLE a (id int);`)
	writeSQL(t, dir, "notes.txt", `not a migration`)

	res, err := migrator.Apply(db, schema, dir)
	if err != nil || !reflect.DeepEqual(res.Applied, []string{"0001_a.sql", "0002_b.sql"}) {
		t.Fatalf("files must apply in name order and ignore non-.sql files: res=%+v err=%v", res, err)
	}

	writeSQL(t, dir, "0003_c.sql", `CREATE TABLE c (id int);`)
	res, err = migrator.Apply(db, schema, dir)
	if err != nil || !reflect.DeepEqual(res.Applied, []string{"0003_c.sql"}) || !reflect.DeepEqual(res.Skipped, []string{"0001_a.sql", "0002_b.sql"}) {
		t.Fatalf("only the new file must apply: res=%+v err=%v", res, err)
	}
	if n := tableCount(t, db, schema, "a", "b", "c"); n != 3 {
		t.Fatalf("%d tables", n)
	}
}

func TestApplyIsAtomic(t *testing.T) {
	db := helpers.Connect(t)
	schema := newName(t, db)
	dir := t.TempDir()
	writeSQL(t, dir, "0001_ok.sql", `CREATE TABLE ok (id int);`)
	writeSQL(t, dir, "0002_bad.sql", `THIS IS NOT SQL;`)

	res, err := migrator.Apply(db, schema, dir)
	if err == nil || !strings.Contains(err.Error(), "0002_bad.sql") {
		t.Fatalf("want an error naming the broken file, got %v", err)
	}
	if len(res.Applied) != 0 || len(res.Skipped) != 0 {
		t.Fatalf("a failed run must report nothing applied: %+v", res)
	}
	if schemaExists(t, db, schema) {
		t.Fatal("the schema was left behind after a failed migration")
	}
}

func TestApplyEdgeCases(t *testing.T) {
	db := helpers.Connect(t)

	t.Run("an empty or missing directory just creates the schema", func(t *testing.T) {
		for name, dir := range map[string]string{"empty": t.TempDir(), "missing": filepath.Join(t.TempDir(), "nope")} {
			schema := newName(t, db)
			res, err := migrator.Apply(db, schema, dir)
			if err != nil || len(res.Applied) != 0 || !schemaExists(t, db, schema) {
				t.Errorf("%s: res=%+v err=%v", name, res, err)
			}
			if tableCount(t, db, schema, "schema_migrations") != 1 {
				t.Errorf("%s: no schema_migrations table", name)
			}
		}
	})

	t.Run("invalid schema names are refused before touching the database", func(t *testing.T) {
		for _, bad := range []string{"", "Bad", "a;b", "has space", strings.Repeat("a", 64)} {
			if _, err := migrator.Apply(db, bad, helpers.MigrationsDir()); err == nil || !strings.Contains(err.Error(), "invalid schema name") {
				t.Errorf("%q: %v", bad, err)
			}
		}
	})

	t.Run("two schemas migrate independently", func(t *testing.T) {
		a, b := newName(t, db), newName(t, db)
		for _, s := range []string{a, b} {
			if res, err := migrator.Apply(db, s, helpers.MigrationsDir()); err != nil || len(res.Applied) != len(realMigrations) {
				t.Fatalf("%s: res=%+v err=%v", s, res, err)
			}
		}
	})
}

func TestDropSchema(t *testing.T) {
	db := helpers.Connect(t)

	t.Run("drops a schema with its tables, and is idempotent", func(t *testing.T) {
		schema := newName(t, db)
		if _, err := migrator.Apply(db, schema, helpers.MigrationsDir()); err != nil {
			t.Fatal(err)
		}
		if err := migrator.DropSchema(db, schema); err != nil {
			t.Fatal(err)
		}
		if schemaExists(t, db, schema) || tableCount(t, db, schema, "jobs") != 0 {
			t.Fatal("schema or tables survived the drop")
		}
		if err := migrator.DropSchema(db, schema); err != nil {
			t.Fatalf("second drop: %v", err)
		}
	})

	t.Run("dropping a schema that never existed is fine", func(t *testing.T) {
		if err := migrator.DropSchema(db, helpers.RandomSchemaName("never_")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("public and invalid names are refused", func(t *testing.T) {
		if err := migrator.DropSchema(db, "public"); err == nil || !strings.Contains(err.Error(), "public") {
			t.Fatalf("public: %v", err)
		}
		if !schemaExists(t, db, "public") {
			t.Fatal("public schema is gone")
		}
		for _, bad := range []string{"", "Bad", "a;drop schema public cascade", "x y"} {
			if err := migrator.DropSchema(db, bad); err == nil || !strings.Contains(err.Error(), "invalid schema name") {
				t.Errorf("%q: %v", bad, err)
			}
		}
	})

	t.Run("it only drops the named schema", func(t *testing.T) {
		keep, drop := newName(t, db), newName(t, db)
		for _, s := range []string{keep, drop} {
			if _, err := migrator.Apply(db, s, helpers.MigrationsDir()); err != nil {
				t.Fatal(err)
			}
		}
		if err := migrator.DropSchema(db, drop); err != nil {
			t.Fatal(err)
		}
		if !schemaExists(t, db, keep) || tableCount(t, db, keep, "jobs") != 1 {
			t.Fatal("the other schema was affected")
		}
	})
}

func TestApplyAndDropFailurePaths(t *testing.T) {
	db := helpers.Connect(t)

	t.Run("a migration file that cannot be read fails the run and rolls everything back", func(t *testing.T) {
		schema := newName(t, db)
		dir := t.TempDir()
		writeSQL(t, dir, "0001_ok.sql", `CREATE TABLE ok (id int);`)
		if err := os.Mkdir(filepath.Join(dir, "0002_dir.sql"), 0o700); err != nil { // matches *.sql but is a directory
			t.Fatal(err)
		}
		if _, err := migrator.Apply(db, schema, dir); err == nil {
			t.Fatal("want an error")
		}
		if schemaExists(t, db, schema) {
			t.Fatal("the schema was left behind")
		}
	})

	t.Run("a malformed directory pattern is an error", func(t *testing.T) {
		schema := newName(t, db)
		if _, err := migrator.Apply(db, schema, filepath.Join(t.TempDir(), "bad[")); err == nil {
			t.Fatal("want an error")
		}
		if schemaExists(t, db, schema) {
			t.Fatal("the schema was created")
		}
	})

	t.Run("a dead database connection is reported, not swallowed", func(t *testing.T) {
		dead := helpers.Connect(t)
		sqlDB, _ := dead.DB()
		_ = sqlDB.Close()
		if _, err := migrator.Apply(dead, helpers.RandomSchemaName("mig_"), helpers.MigrationsDir()); err == nil {
			t.Error("Apply: want an error")
		}
		if err := migrator.DropSchema(dead, helpers.RandomSchemaName("mig_")); err == nil {
			t.Error("DropSchema: want an error")
		}
	})
}
