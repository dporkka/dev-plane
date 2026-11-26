package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/ai-dev-control-plane/models"
)

// These exact subjects are outside every legacy stream wildcard.
const (
	GiteaTaskApproved     = "gitea.exec.tasks.approved"
	GiteaRunTriggered     = "gitea.exec.runs.triggered"
	GiteaApprovalApproved = "gitea.exec.approval.approved"
	GiteaApprovalRejected = "gitea.exec.approval.rejected"
)

var ErrExecutionRoute = errors.New("execution command route unavailable")

// WithExecutionRouting must be called during startup, before publishing.
// Raw buses remain available for administrative/fixture operations.
func (b *Bus) WithExecutionRouting(db *sql.DB) *Bus {
	b.executionRoutingDB = db
	b.executionRoutingEnabled = true
	return b
}

func IsExecutionCommand(subject string) bool {
	switch subject {
	case TaskApproved, RunTriggered, ApprovalApproved, ApprovalRejected:
		return true
	default:
		return false
	}
}

// ExecutionCommand translates only known dedicated subjects to their original
// command. It never interprets arbitrary prefixes or payload-supplied lanes.
func ExecutionCommand(subject string) (string, bool) {
	switch subject {
	case GiteaTaskApproved:
		return TaskApproved, true
	case GiteaRunTriggered:
		return RunTriggered, true
	case GiteaApprovalApproved:
		return ApprovalApproved, true
	case GiteaApprovalRejected:
		return ApprovalRejected, true
	default:
		return subject, IsExecutionCommand(subject)
	}
}

// ResolveExecutionSubject classifies from the active durable tenant chain,
// not event metadata or credentials. Unbound Gitea tasks still route only to
// Gitea; the binding verification at execution is a separate authority gate.
func ResolveExecutionSubject(ctx context.Context, db *sql.DB, command string, data []byte) (string, error) {
	if db == nil || !IsExecutionCommand(command) {
		return "", ErrExecutionRoute
	}
	var event struct {
		TaskID       string `json:"task_id"`
		RunID        string `json:"run_id"`
		ApprovalID   string `json:"approval_id"`
		AgentRunID   string `json:"agent_run_id"`
		ApprovalType string `json:"approval_type"`
		Response     string `json:"response"`
	}
	if err := json.Unmarshal(data, &event); err != nil || strings.TrimSpace(event.TaskID) == "" || strings.TrimSpace(event.TaskID) != event.TaskID {
		return "", ErrExecutionRoute
	}
	// One statement, so the task/tenant identity and command linkage are
	// resolved from the same committed database snapshot.
	query := `
		SELECT r.settings,b.repository_id
		FROM tasks t
		JOIN repositories r ON r.id=t.repository_id AND r.project_id=t.project_id AND r.deleted_at IS NULL
		JOIN projects p ON p.id=t.project_id AND p.deleted_at IS NULL
		JOIN organizations o ON o.id=p.organization_id AND o.deleted_at IS NULL
		LEFT JOIN gitea_repository_bindings b ON b.repository_id=r.id
		WHERE t.id=$1 AND t.deleted_at IS NULL
	`
	args := []any{event.TaskID}
	switch command {
	case RunTriggered:
		if strings.TrimSpace(event.RunID) == "" {
			return "", ErrExecutionRoute
		}
		query += " AND EXISTS (SELECT 1 FROM agent_runs ar WHERE ar.id=$2 AND ar.task_id=t.id)"
		args = append(args, event.RunID)
	case ApprovalApproved, ApprovalRejected:
		response := "approved"
		if command == ApprovalRejected {
			response = "rejected"
		}
		if event.ApprovalID == "" || event.ApprovalType == "" || event.Response != response {
			return "", ErrExecutionRoute
		}
		// CAST avoids UUID/text comparison differences between SQLite and PG.
		query += ` AND EXISTS (SELECT 1 FROM approvals a WHERE a.id=$2 AND a.task_id=t.id
			AND a.approval_type=$3 AND a.response=$4 AND COALESCE(CAST(a.agent_run_id AS TEXT),'')=$5
			AND (a.agent_run_id IS NULL OR EXISTS (SELECT 1 FROM agent_runs ar WHERE ar.id=a.agent_run_id AND ar.task_id=t.id)))`
		args = append(args, event.ApprovalID, event.ApprovalType, response, event.AgentRunID)
	}
	var settings, reserved sql.NullString
	if err := db.QueryRowContext(ctx, query, args...).Scan(&settings, &reserved); err != nil {
		return "", ErrExecutionRoute
	}
	forge, err := models.ParseRepositoryForgeSettings([]byte(settings.String))
	if err != nil || (reserved.Valid && forge.Provider != models.ForgeProviderGitea) {
		return "", ErrExecutionRoute
	}
	if forge.Provider == models.ForgeProviderGitea {
		return "gitea.exec." + command, nil
	}
	return command, nil
}

// VerifyExecutionDelivery performs a second routing check before invoking a
// handler. Old/misrouted messages stay on the original queue for operator
// reconciliation; there is no cross-lane forwarding.
func VerifyExecutionDelivery(ctx context.Context, db *sql.DB, subject string, data []byte, giteaWorker bool) error {
	command, ok := ExecutionCommand(subject)
	if !ok || (subject != command) != giteaWorker {
		return ErrExecutionRoute
	}
	expected, err := ResolveExecutionSubject(ctx, db, command, data)
	if err != nil || expected != subject {
		return ErrExecutionRoute
	}
	return nil
}
