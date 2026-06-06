// Package db provides a unified database interface that works with both
// SQLite (local development) and PostgreSQL (production).
//
// The DB struct wraps *sql.DB with driver-aware helpers for JSON columns,
// timestamps, UUIDs, and transactions.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pressly/goose/v3"
)

// DB wraps sql.DB with driver information and driver-aware helpers.
type DB struct {
	*sql.DB
	Driver string // "sqlite" or "postgres"
}

// New creates a new DB instance by auto-detecting the database type from the URL.
// Supports SQLite (file: prefix or absolute path) and PostgreSQL (postgres:// or postgresql://).
func New(databaseURL string) (*DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("database URL is empty")
	}
	if databaseURL == ":memory:" || strings.HasPrefix(databaseURL, "file:") || strings.HasPrefix(databaseURL, "/") {
		return newSQLite(databaseURL)
	}
	if strings.Contains(databaseURL, "postgres") {
		return newPostgres(databaseURL)
	}
	return nil, fmt.Errorf("unsupported database URL scheme: %s", databaseURL)
}

// RunMigrations runs all pending Goose migrations in the given directory.
func (db *DB) RunMigrations(migrationsDir string) error {
	if err := goose.SetDialect(gooseDialect(db.Driver)); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	return goose.Up(db.DB, migrationsDir)
}

// RunMigrationsDown rolls back all migrations (use with caution — mainly for testing).
func (db *DB) RunMigrationsDown(migrationsDir string) error {
	if err := goose.SetDialect(gooseDialect(db.Driver)); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	return goose.Down(db.DB, migrationsDir)
}

func gooseDialect(driver string) string {
	if driver == "sqlite" {
		return "sqlite3"
	}
	return driver
}

// ---------------------------------------------------------------------------
// Driver-aware SQL type helpers
// ---------------------------------------------------------------------------

// JSONType returns the column type for JSON data suitable for the driver.
// Returns "JSONB" for PostgreSQL, "TEXT" for SQLite.
func (db *DB) JSONType() string {
	if db.Driver == "sqlite" {
		return "TEXT"
	}
	return "JSONB"
}

// NowFunc returns the SQL function for the current timestamp.
// Returns "CURRENT_TIMESTAMP" for SQLite, "NOW()" for PostgreSQL.
func (db *DB) NowFunc() string {
	if db.Driver == "sqlite" {
		return "CURRENT_TIMESTAMP"
	}
	return "NOW()"
}

// UUIDType returns the column type for UUID values.
// Both engines store UUIDs as TEXT for maximum portability.
func (db *DB) UUIDType() string {
	return "UUID"
}

// TimestampType returns the column type for timestamps.
func (db *DB) TimestampType() string {
	if db.Driver == "sqlite" {
		return "DATETIME"
	}
	return "TIMESTAMPTZ"
}

// JSONColumn returns a complete column definition for a JSON column with the given name.
func (db *DB) JSONColumn(name string) string {
	return fmt.Sprintf("%s %s", name, db.JSONType())
}

// Placeholder returns the appropriate parameter placeholder for the driver.
// SQLite uses "?", PostgreSQL uses "$N" positional parameters.
// This is a convenience wrapper — most callers should use sqlc-generated code.
func (db *DB) Placeholder(n int) string {
	if db.Driver == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// ---------------------------------------------------------------------------
// Transaction helpers
// ---------------------------------------------------------------------------

// TxFunc is a function that executes within a database transaction.
type TxFunc func(ctx context.Context, tx *sql.Tx) error

// WithTx executes the given function within a transaction.
// The transaction is committed if the function returns nil, otherwise it is rolled back.
func (db *DB) WithTx(ctx context.Context, fn TxFunc) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx fn error: %w; rollback error: %v", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// WithTxOpts executes the given function within a transaction with custom options.
func (db *DB) WithTxOpts(ctx context.Context, opts *sql.TxOptions, fn TxFunc) error {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx fn error: %w; rollback error: %v", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Health check
// ---------------------------------------------------------------------------

// Ping verifies the database connection is alive.
func (db *DB) Ping(ctx context.Context) error {
	return db.DB.PingContext(ctx)
}

// Close closes the underlying database connection.
func (db *DB) Close() error {
	return db.DB.Close()
}
