package dbconfig_test

import (
	"net/url"
	"testing"

	"recurringjob/internal/common/dbconfig"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME"} {
		t.Setenv(k, kv[k])
	}
}

func TestDatabaseURLDefaults(t *testing.T) {
	setEnv(t, nil)
	if got, want := dbconfig.DatabaseURL(), "postgres://postgres:@localhost:5432/postgres?sslmode=disable"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDatabaseURLFromEnv(t *testing.T) {
	setEnv(t, map[string]string{"DB_HOST": "db.internal", "DB_PORT": "6543", "DB_USER": "app", "DB_PASSWORD": "secret", "DB_NAME": "JobRecurring"})
	u, err := url.Parse(dbconfig.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if u.Host != "db.internal:6543" || u.User.Username() != "app" || pw != "secret" || u.Path != "/JobRecurring" {
		t.Fatalf("unexpected URL %s", u.Redacted())
	}
}

func TestDatabaseURLEscapesPassword(t *testing.T) {
	const password = "Pa*ss@:/#?%"
	setEnv(t, map[string]string{"DB_PASSWORD": password})
	u, err := url.Parse(dbconfig.DatabaseURL())
	if err != nil {
		t.Fatalf("URL does not parse: %v", err)
	}
	if got, _ := u.User.Password(); got != password {
		t.Fatalf("password round-trip: got %q, want %q", got, password)
	}
	if u.Host != "localhost:5432" {
		t.Fatalf("special characters leaked into host: %q", u.Host)
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("DBCONFIG_TEST_KEY", "")
	if got := dbconfig.Env("DBCONFIG_TEST_KEY", "fallback"); got != "fallback" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("DBCONFIG_TEST_KEY", "set")
	if got := dbconfig.Env("DBCONFIG_TEST_KEY", "fallback"); got != "set" {
		t.Fatalf("got %q", got)
	}
}
