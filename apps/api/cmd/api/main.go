// Package main is the entry point for the API service.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/config"
	"github.com/ai-dev-control-plane/api/internal/server"
)

func main() {
	// Load .env file if present
	godotenv.Load()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Setup logger
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	logger.Info("starting api service", "port", cfg.Port)

	// Initialize database
	db, err := initDB(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run migrations
	if err := runMigrations(db, cfg.DatabaseURL); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	logger.Info("database initialized and migrations applied")

	// Create and start server
	srv := server.New(cfg, db, logger)

	// Handle graceful shutdown on SIGTERM/SIGINT
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	addr := fmt.Sprintf(":%s", cfg.Port)
	go func() {
		logger.Info("server listening", "addr", addr)
		if err := srv.Start(addr); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped gracefully")
}

// initDB initializes a database connection based on the DATABASE_URL.
func initDB(databaseURL string) (*sql.DB, error) {
	driverName, dsn := parseDatabaseURL(databaseURL)

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure connection pool
	if driverName == "sqlite3" {
		db.SetMaxOpenConns(1) // SQLite requires single writer
		db.SetMaxIdleConns(1)
		if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
		}
	} else {
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(5)
	}

	return db, nil
}

// parseDatabaseURL determines the driver and DSN from the DATABASE_URL.
func parseDatabaseURL(databaseURL string) (driver, dsn string) {
	// SQLite
	if strings.HasPrefix(databaseURL, "file:") || strings.HasPrefix(databaseURL, "/") {
		dsn = databaseURL
		if strings.HasPrefix(databaseURL, "file:") {
			_ = ensureSQLiteParentDir(databaseURL)
			// Ensure WAL mode and foreign keys are enabled
			if !strings.Contains(dsn, "_journal_mode") {
				if strings.Contains(dsn, "?") {
					dsn += "&_journal_mode=WAL"
				} else {
					dsn += "?_journal_mode=WAL"
				}
			}
			if !strings.Contains(dsn, "_foreign_keys") {
				if strings.Contains(dsn, "?") {
					dsn += "&_foreign_keys=on"
				} else {
					dsn += "?_foreign_keys=on"
				}
			}
		}
		return "sqlite3", dsn
	}

	// Postgres
	if strings.Contains(databaseURL, "postgres") {
		return "postgres", databaseURL
	}

	// Try to parse as URL
	if u, err := url.Parse(databaseURL); err == nil {
		switch u.Scheme {
		case "postgres", "postgresql":
			return "postgres", databaseURL
		case "sqlite", "file":
			return "sqlite3", databaseURL
		}
	}

	// Default to sqlite3
	return "sqlite3", databaseURL
}

// ensureSQLiteParentDir creates the parent directory for a file: SQLite URL.
func ensureSQLiteParentDir(databaseURL string) error {
	path := databaseURL
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

// runMigrations runs database migrations using goose.
func runMigrations(db *sql.DB, databaseURL string) error {
	driverName, _ := parseDatabaseURL(databaseURL)

	// Convert driver name to goose dialect
	var gooseDialect string
	switch driverName {
	case "sqlite3":
		gooseDialect = "sqlite3"
	case "postgres":
		gooseDialect = "postgres"
	default:
		gooseDialect = "sqlite3"
	}

	// Migration files are in packages/db/migrations relative to repo root
	// When running from apps/api, go up two levels then into packages/db
	migrationsDir := "../../packages/db/migrations"

	// Check if running in different working directory
	if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
		// Try current directory / packages/db/migrations
		migrationsDir = "packages/db/migrations"
		if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
			// Try from apps/api working directory
			migrationsDir = "../packages/db/migrations"
			if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
				// Fallback: skip migrations if directory not found
				slog.Default().Warn("migrations directory not found, skipping", "dir", migrationsDir)
				return nil
			}
		}
	}

	if err := goose.SetDialect(gooseDialect); err != nil {
		return fmt.Errorf("failed to set goose dialect: %w", err)
	}

	if err := goose.Up(db, migrationsDir); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}
