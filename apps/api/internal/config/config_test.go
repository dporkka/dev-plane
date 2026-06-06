package config

import (
	"os"
	"testing"
)

func assertEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLoad_Defaults(t *testing.T) {
	// Clear relevant env vars to test defaults
	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("ALLOWED_ORIGINS")
	os.Unsetenv("SECRET_ENCRYPTION_KEYS")

	cfg := Load()
	assertEqual(t, cfg.Port, "8080")
	assertEqual(t, cfg.DatabaseURL, "file:./data/dev.db?_journal_mode=WAL")
	assertEqual(t, cfg.LogLevel, "info")
	assertEqual(t, cfg.SecretKeys, "")
	assertEqual(t, len(cfg.AllowedOrigins), 1)
	assertEqual(t, cfg.AllowedOrigins[0], "http://localhost:3000")
}

func TestLoad_FromEnv(t *testing.T) {
	os.Setenv("PORT", "9090")
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("SECRET_ENCRYPTION_KEYS", "primary:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("SECRET_ENCRYPTION_KEYS")
	}()

	cfg := Load()
	assertEqual(t, cfg.Port, "9090")
	assertEqual(t, cfg.DatabaseURL, "postgres://user:pass@localhost/db")
	assertEqual(t, cfg.LogLevel, "debug")
	assertEqual(t, cfg.SecretKeys, "primary:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
}

func TestLoad_CustomPort(t *testing.T) {
	os.Setenv("PORT", "4000")
	defer os.Unsetenv("PORT")

	cfg := Load()
	assertEqual(t, cfg.Port, "4000")
}

func TestLoad_CustomDatabaseURL(t *testing.T) {
	os.Setenv("DATABASE_URL", "postgresql://admin:secret@db.example.com:5432/prod")
	defer os.Unsetenv("DATABASE_URL")

	cfg := Load()
	assertEqual(t, cfg.DatabaseURL, "postgresql://admin:secret@db.example.com:5432/prod")
}

func TestLoad_CustomLogLevel(t *testing.T) {
	os.Setenv("LOG_LEVEL", "warn")
	defer os.Unsetenv("LOG_LEVEL")

	cfg := Load()
	assertEqual(t, cfg.LogLevel, "warn")
}
