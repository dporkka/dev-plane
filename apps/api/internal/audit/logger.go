// Package audit provides audit logging for the capability kernel and other
// security-critical operations.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Logger writes audit records to persistent storage.
type Logger struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewLogger creates an audit logger backed by the given database.
func NewLogger(db *sql.DB, logger *slog.Logger) *Logger {
	return &Logger{db: db, logger: logger}
}

// LogEvent records a single audit event.
func (l *Logger) LogEvent(ctx context.Context, orgID, actorType, actorID, action, resourceType, resourceID string, details map[string]any) error {
	detailsJSON, _ := json.Marshal(details)
	id := uuid.New().String()
	now := time.Now().UTC()

	_, err := l.db.ExecContext(ctx, `
		INSERT INTO audit_logs (id, organization_id, actor_type, actor_id, action, resource_type, resource_id, details, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, id, orgID, actorType, actorID, action, resourceType, resourceID, string(detailsJSON), now)

	if err != nil {
		l.logger.Error("failed to write audit log", "error", err, "action", action, "org_id", orgID)
		return fmt.Errorf("write audit log: %w", err)
	}

	return nil
}

// LogCapabilityCheck records a capability evaluation result.
func (l *Logger) LogCapabilityCheck(ctx context.Context, orgID, actorID, operation, resource string, effect string, allowed bool, reason string) error {
	actorType := "system"
	if actorID != "" {
		actorType = "agent"
	}
	return l.LogCapabilityCheckForActor(ctx, orgID, actorType, actorID, operation, resource, effect, allowed, reason)
}

// LogCapabilityCheckForActor records a capability evaluation result with an
// explicit actor type.
func (l *Logger) LogCapabilityCheckForActor(ctx context.Context, orgID, actorType, actorID, operation, resource string, effect string, allowed bool, reason string) error {
	details := map[string]any{
		"operation": operation,
		"resource":  resource,
		"effect":    effect,
		"allowed":   allowed,
		"reason":    reason,
	}
	if actorType == "" {
		actorType = "system"
	}
	return l.LogEvent(ctx, orgID, actorType, actorID, "capability_check", "capability", resource, details)
}

// LogBudgetViolation records a budget constraint violation.
func (l *Logger) LogBudgetViolation(ctx context.Context, orgID, actorID, violationType, reason string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["violation_type"] = violationType
	details["reason"] = reason
	return l.LogEvent(ctx, orgID, "system", actorID, "budget_violation", "budget", "", details)
}

// LogModelCall records a model API call for cost tracking and audit.
func (l *Logger) LogModelCall(ctx context.Context, orgID, runID, taskID, model, provider string, promptTokens, completionTokens int, cost float64, latencyMs int) error {
	details := map[string]any{
		"run_id":            runID,
		"task_id":           taskID,
		"model":             model,
		"provider":          provider,
		"prompt_tokens":     promptTokens,
		"completion_tokens": completionTokens,
		"cost":              cost,
		"latency_ms":        latencyMs,
	}
	return l.LogEvent(ctx, orgID, "agent", runID, "model_call", "model", model, details)
}
