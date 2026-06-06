package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
)

// newSQLite creates a SQLite connection with WAL mode and foreign keys enabled.
func newSQLite(url string) (*DB, error) {
	if err := ensureSQLiteParentDir(url); err != nil {
		return nil, err
	}
	if url != ":memory:" && !strings.Contains(url, "mode=memory") && !strings.Contains(url, "_journal_mode") {
		separator := "?"
		if strings.Contains(url, "?") {
			separator = "&"
		}
		url = url + separator + "_journal_mode=WAL&_foreign_keys=on"
	}

	sqlDB, err := sql.Open("sqlite3", url)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	return &DB{DB: sqlDB, Driver: "sqlite"}, nil
}

func ensureSQLiteParentDir(url string) error {
	if strings.Contains(url, "mode=memory") {
		return nil
	}
	path := url
	if strings.HasPrefix(path, "file:") {
		path = strings.TrimPrefix(path, "file:")
	}
	if idx := strings.IndexAny(path, "?#"); idx >= 0 {
		path = path[:idx]
	}
	if path == "" || path == ":memory:" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create sqlite directory: %w", err)
	}
	return nil
}

// newPostgres creates a PostgreSQL connection with service-oriented pool defaults.
func newPostgres(url string) (*DB, error) {
	sqlDB, err := sql.Open("postgres", url)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &DB{DB: sqlDB, Driver: "postgres"}, nil
}
