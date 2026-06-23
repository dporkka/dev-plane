package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunMigrationsSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "migrations.db")
	database, err := New("file:" + dbPath)
	if err != nil {
		t.Fatalf("New() sqlite error: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations(sqlite) error: %v", err)
	}

	for _, table := range []string{
		"organizations",
		"users",
		"projects",
		"repositories",
		"workspaces",
		"tasks",
		"agent_runs",
		"agent_steps",
		"agent_messages",
		"review_reports",
		"audit_logs",
		"integrations",
		"secret_references",
		"secret_values",
		"approvals",
		"policies",
		"model_usage",
		"pull_requests",
	} {
		var name string
		if err := database.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s missing after migrations: %v", table, err)
		}
	}
}

func TestRunMigrationsPostgres(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set POSTGRES_TEST_DATABASE_URL to run live Postgres migration verification")
	}
	database, err := New(url)
	if err != nil {
		t.Fatalf("New() postgres error: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations(postgres) error: %v", err)
	}
}
