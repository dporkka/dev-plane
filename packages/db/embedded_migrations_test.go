package db

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func TestRunEmbeddedMigrationsCreatesSchema(t *testing.T) {
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer database.Close()

	if err := RunEmbeddedMigrations(database, "sqlite3"); err != nil {
		t.Fatalf("RunEmbeddedMigrations() error: %v", err)
	}

	for _, table := range []string{"organizations", "projects", "tasks", "agent_runs"} {
		var name string
		if err := database.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name); err != nil {
			t.Fatalf("table %s missing after embedded migrations: %v", table, err)
		}
	}
}
