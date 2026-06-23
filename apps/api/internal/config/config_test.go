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

func setValidJWTSecret(t *testing.T) {
	t.Helper()
	os.Setenv("JWT_SECRET", "this-is-a-very-long-development-secret-key")
}

func TestLoad_Defaults(t *testing.T) {
	setValidJWTSecret(t)
	defer os.Unsetenv("JWT_SECRET")

	// Clear relevant env vars to test defaults
	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_URL")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("ALLOWED_ORIGINS")
	os.Unsetenv("SECRET_ENCRYPTION_KEYS")
	os.Unsetenv("GITHUB_APP_WEBHOOK_SECRET")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	assertEqual(t, cfg.Port, "8080")
	assertEqual(t, cfg.DatabaseURL, "file:./data/dev.db?_journal_mode=WAL")
	assertEqual(t, cfg.LogLevel, "info")
	assertEqual(t, cfg.SecretKeys, "")
	assertEqual(t, cfg.GitHubWebhookSecret, "")
	assertEqual(t, len(cfg.AllowedOrigins), 1)
	assertEqual(t, cfg.AllowedOrigins[0], "http://localhost:3000")
}

func TestLoad_FromEnv(t *testing.T) {
	setValidJWTSecret(t)
	os.Setenv("PORT", "9090")
	os.Setenv("DATABASE_URL", "postgres://user:pass@localhost/db")
	os.Setenv("LOG_LEVEL", "debug")
	os.Setenv("SECRET_ENCRYPTION_KEYS", "primary:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	os.Setenv("GITHUB_APP_WEBHOOK_SECRET", "webhook-secret")
	defer func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("PORT")
		os.Unsetenv("DATABASE_URL")
		os.Unsetenv("LOG_LEVEL")
		os.Unsetenv("SECRET_ENCRYPTION_KEYS")
		os.Unsetenv("GITHUB_APP_WEBHOOK_SECRET")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	assertEqual(t, cfg.Port, "9090")
	assertEqual(t, cfg.DatabaseURL, "postgres://user:pass@localhost/db")
	assertEqual(t, cfg.LogLevel, "debug")
	assertEqual(t, cfg.SecretKeys, "primary:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	assertEqual(t, cfg.GitHubWebhookSecret, "webhook-secret")
}

func TestLoad_CustomPort(t *testing.T) {
	setValidJWTSecret(t)
	os.Setenv("PORT", "4000")
	defer func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("PORT")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	assertEqual(t, cfg.Port, "4000")
}

func TestLoad_CustomDatabaseURL(t *testing.T) {
	setValidJWTSecret(t)
	os.Setenv("DATABASE_URL", "postgresql://admin:secret@db.example.com:5432/prod")
	defer func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("DATABASE_URL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	assertEqual(t, cfg.DatabaseURL, "postgresql://admin:secret@db.example.com:5432/prod")
}

func TestLoad_CustomLogLevel(t *testing.T) {
	setValidJWTSecret(t)
	os.Setenv("LOG_LEVEL", "warn")
	defer func() {
		os.Unsetenv("JWT_SECRET")
		os.Unsetenv("LOG_LEVEL")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	assertEqual(t, cfg.LogLevel, "warn")
}

func TestLoad_RequiresJWTSecret(t *testing.T) {
	os.Unsetenv("JWT_SECRET")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing JWT_SECRET")
	}
}

func TestLoad_RejectsShortJWTSecret(t *testing.T) {
	os.Setenv("JWT_SECRET", "short")
	defer os.Unsetenv("JWT_SECRET")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for short JWT_SECRET")
	}
}
