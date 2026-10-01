package db

import (
	"database/sql"
	"embed"
	"fmt"
	"sync"

	"github.com/pressly/goose/v3"
)

// embeddedMigrations makes release binaries self-contained. Goose resolves the
// migration directory relative to this filesystem when RunEmbeddedMigrations
// is called.
//
//go:embed migrations/*.sql
var embeddedMigrations embed.FS

var embeddedMigrationMu sync.Mutex

// RunEmbeddedMigrations applies all pending SQL migrations from the files
// compiled into this package. The mutex protects Goose's process-global base
// filesystem and dialect configuration from concurrent mutation in tests or
// multi-database callers.
func RunEmbeddedMigrations(database *sql.DB, dialect string) error {
	if database == nil {
		return fmt.Errorf("database is nil")
	}

	embeddedMigrationMu.Lock()
	defer embeddedMigrationMu.Unlock()

	goose.SetBaseFS(embeddedMigrations)
	defer goose.SetBaseFS(nil)

	if err := goose.SetDialect(dialect); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(database, "migrations"); err != nil {
		return fmt.Errorf("apply embedded migrations: %w", err)
	}
	return nil
}
