package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// setupTestDB creates an in-memory SQLite DB with the audit_logs table.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE audit_logs (
			id TEXT PRIMARY KEY,
			organization_id TEXT,
			actor_type TEXT,
			actor_id TEXT,
			action TEXT,
			resource_type TEXT,
			resource_id TEXT,
			details TEXT,
			created_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create audit_logs table: %v", err)
	}

	return db
}

// TestLogger_LogEvent verifies audit event logging.
func TestLogger_LogEvent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := NewLogger(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ctx := context.Background()

	details := map[string]any{
		"key": "value",
		"num": 42,
	}

	err := logger.LogEvent(ctx, "org-1", "agent", "actor-1", "read_file", "file", "main.go", details)
	if err != nil {
		t.Fatalf("LogEvent() error: %v", err)
	}

	// Verify the event was logged
	var count int
	err = db.QueryRow(
		"SELECT COUNT(*) FROM audit_logs WHERE organization_id = ? AND action = ? AND resource_type = ?",
		"org-1", "read_file", "file",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 audit log, got %d", count)
	}

	// Verify details JSON was stored correctly
	var detailsJSON string
	err = db.QueryRow(
		"SELECT details FROM audit_logs WHERE organization_id = ? AND action = ?",
		"org-1", "read_file",
	).Scan(&detailsJSON)
	if err != nil {
		t.Fatalf("query details: %v", err)
	}

	var storedDetails map[string]any
	if err := json.Unmarshal([]byte(detailsJSON), &storedDetails); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if storedDetails["key"] != "value" {
		t.Errorf("details.key = %v, want 'value'", storedDetails["key"])
	}
	if storedDetails["num"] != float64(42) {
		t.Errorf("details.num = %v, want 42", storedDetails["num"])
	}
}

// TestLogger_LogEvent_Error verifies error handling on insert failure.
func TestLogger_LogEvent_Error(t *testing.T) {
	// Use a closed database to force errors
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	// Don't create the table - this will cause insert to fail

	logger := NewLogger(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ctx := context.Background()

	err = logger.LogEvent(ctx, "org-1", "agent", "actor-1", "read_file", "file", "main.go", nil)
	if err == nil {
		t.Error("expected error when insert fails")
	}
	db.Close()
}

// TestLogger_LogCapabilityCheck verifies capability check logging.
func TestLogger_LogCapabilityCheck(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := NewLogger(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ctx := context.Background()

	err := logger.LogCapabilityCheck(ctx, "org-1", "user-1", "read_file", "main.go", "allow", true, "allowed: read_file on main.go")
	if err != nil {
		t.Fatalf("LogCapabilityCheck() error: %v", err)
	}

	// Verify the check was logged
	var actorType, action, detailsJSON string
	err = db.QueryRow(
		"SELECT actor_type, action, details FROM audit_logs WHERE organization_id = ? AND actor_id = ?",
		"org-1", "user-1",
	).Scan(&actorType, &action, &detailsJSON)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if actorType != "agent" {
		t.Errorf("actor_type = %q, want 'agent'", actorType)
	}
	if action != "capability_check" {
		t.Errorf("action = %q, want 'capability_check'", action)
	}

	var details map[string]any
	if err := json.Unmarshal([]byte(detailsJSON), &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if details["operation"] != "read_file" {
		t.Errorf("details.operation = %v, want 'read_file'", details["operation"])
	}
	if details["effect"] != "allow" {
		t.Errorf("details.effect = %v, want 'allow'", details["effect"])
	}
	if details["allowed"] != true {
		t.Errorf("details.allowed = %v, want true", details["allowed"])
	}
}

// TestLogger_LogCapabilityCheck_SystemActor verifies actor_type is "system" when actorID is empty.
func TestLogger_LogCapabilityCheck_SystemActor(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := NewLogger(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ctx := context.Background()

	err := logger.LogCapabilityCheck(ctx, "org-1", "", "read_file", "main.go", "allow", true, "system action")
	if err != nil {
		t.Fatalf("LogCapabilityCheck() error: %v", err)
	}

	var actorType string
	err = db.QueryRow(
		"SELECT actor_type FROM audit_logs WHERE organization_id = ?",
		"org-1",
	).Scan(&actorType)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if actorType != "system" {
		t.Errorf("actor_type = %q, want 'system' (empty actorID)", actorType)
	}
}

// TestLogger_LogBudgetViolation verifies budget violation logging.
func TestLogger_LogBudgetViolation(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := NewLogger(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ctx := context.Background()

	details := map[string]any{
		"max_cost": 1.00,
		"actual":   1.50,
	}

	err := logger.LogBudgetViolation(ctx, "org-1", "run-1", "max_cost_per_run", "cost exceeded budget", details)
	if err != nil {
		t.Fatalf("LogBudgetViolation() error: %v", err)
	}

	// Verify the violation was logged
	var action, resourceType, detailsJSON string
	err = db.QueryRow(
		"SELECT action, resource_type, details FROM audit_logs WHERE organization_id = ? AND actor_id = ?",
		"org-1", "run-1",
	).Scan(&action, &resourceType, &detailsJSON)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if action != "budget_violation" {
		t.Errorf("action = %q, want 'budget_violation'", action)
	}
	if resourceType != "budget" {
		t.Errorf("resource_type = %q, want 'budget'", resourceType)
	}

	var storedDetails map[string]any
	if err := json.Unmarshal([]byte(detailsJSON), &storedDetails); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if storedDetails["violation_type"] != "max_cost_per_run" {
		t.Errorf("violation_type = %v, want 'max_cost_per_run'", storedDetails["violation_type"])
	}
	if storedDetails["max_cost"] != 1.00 {
		t.Errorf("max_cost = %v, want 1.00", storedDetails["max_cost"])
	}
}

// TestLogger_LogBudgetViolation_NilDetails verifies nil details are handled.
func TestLogger_LogBudgetViolation_NilDetails(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := NewLogger(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ctx := context.Background()

	err := logger.LogBudgetViolation(ctx, "org-1", "run-1", "max_daily_spend", "daily spend exceeded", nil)
	if err != nil {
		t.Fatalf("LogBudgetViolation() error: %v", err)
	}

	var detailsJSON string
	err = db.QueryRow(
		"SELECT details FROM audit_logs WHERE organization_id = ?",
		"org-1",
	).Scan(&detailsJSON)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	var storedDetails map[string]any
	if err := json.Unmarshal([]byte(detailsJSON), &storedDetails); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if storedDetails["violation_type"] != "max_daily_spend" {
		t.Errorf("violation_type = %v, want 'max_daily_spend'", storedDetails["violation_type"])
	}
	if storedDetails["reason"] != "daily spend exceeded" {
		t.Errorf("reason = %v, want 'daily spend exceeded'", storedDetails["reason"])
	}
}

// TestLogger_LogModelCall verifies model call logging.
func TestLogger_LogModelCall(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := NewLogger(db, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	ctx := context.Background()

	err := logger.LogModelCall(ctx, "org-1", "run-1", "task-1", "gpt-4o", "openai", 1000, 500, 0.05, 2500)
	if err != nil {
		t.Fatalf("LogModelCall() error: %v", err)
	}

	var count int
	var action, resourceType, resourceID string
	err = db.QueryRow(
		"SELECT action, resource_type, resource_id FROM audit_logs WHERE organization_id = ? AND actor_id = ?",
		"org-1", "run-1",
	).Scan(&action, &resourceType, &resourceID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if action != "model_call" {
		t.Errorf("action = %q, want 'model_call'", action)
	}
	if resourceType != "model" {
		t.Errorf("resource_type = %q, want 'model'", resourceType)
	}
	if resourceID != "gpt-4o" {
		t.Errorf("resource_id = %q, want 'gpt-4o'", resourceID)
	}

	// Verify count
	err = db.QueryRow("SELECT COUNT(*) FROM audit_logs WHERE organization_id = ?", "org-1").Scan(&count)
	if err != nil {
		t.Fatalf("query count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 audit log, got %d", count)
	}
}

// TestNewLogger verifies logger creation.
func TestNewLogger(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	logger := NewLogger(db, nil)
	if logger == nil {
		t.Fatal("NewLogger() returned nil")
	}
	if logger.db != db {
		t.Error("db not set correctly")
	}
	if logger.logger != nil {
		t.Error("expected nil logger")
	}
}
