// Package dbconfig builds the database connection settings from the
// environment, shared by the API and the migration tool.
package dbconfig

import (
	"net"
	"net/url"
	"os"
)

// Env returns the environment variable key, or fallback when it is empty.
func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// DatabaseURL builds a postgres:// URL from DB_HOST, DB_PORT, DB_USER,
// DB_PASSWORD and DB_NAME. net/url escapes special characters in the
// credentials.
func DatabaseURL() string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(Env("DB_USER", "postgres"), os.Getenv("DB_PASSWORD")),
		Host:     net.JoinHostPort(Env("DB_HOST", "localhost"), Env("DB_PORT", "5432")),
		Path:     Env("DB_NAME", "postgres"),
		RawQuery: "sslmode=disable",
	}
	return u.String()
}
