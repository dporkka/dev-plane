package agentapproval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
	"github.com/google/uuid"
)

// Publisher is the event-bus subset used by the approval bridge.
type Publisher interface {
	Publish(subject string, data []byte) error
}

// Ownership binds a provider-neutral runtime thread to Dev Plane's operational run.
type Ownership struct {
	TaskID     string `json:"task_id"`
	AgentRunID string `json:"agent_run_id"`
}

// Bridge converts durable runtime approval items into Dev Plane approval records.
type Bridge struct {
	db        *sql.DB
	publisher Publisher
	now       func() time.Time
}

// NewBridge creates a runtime approval bridge.
func NewBridge(db *sql.DB, publisher Publisher) *Bridge {
	return &Bridge{
		db:        db,
		publisher: publisher,
		now:       func() time.Time { return time.Now().UTC() },
	}
}

// HandleEvent implements agentruntime.EventSink.
func (b *Bridge) HandleEvent(ctx context.Context, thread agentruntime.Thread, event agentruntime.Event) error {
	if event.Type != agentruntime.EventTypeItemStarted || event.Item == nil ||
		event.Item.Type != agentruntime.ItemTypeApproval ||
		event.Item.Status != agentruntime.ItemStatusPending ||
		event.Status != agentruntime.TurnStatusPausedApproval {
		return nil
	}
	if b == nil || b.db == nil {
		return fmt.Errorf("agent runtime approval bridge database is not configured")
	}

	var ownership Ownership
	if err := json.Unmarshal(thread.Metadata, &ownership); err != nil {
		return fmt.Errorf("decode runtime thread ownership: %w", err)
	}
	ownership.TaskID = strings.TrimSpace(ownership.TaskID)
	ownership.AgentRunID = strings.TrimSpace(ownership.AgentRunID)
	if ownership.TaskID == "" || ownership.AgentRunID == "" {
		return fmt.Errorf("runtime thread %s is missing task/run ownership", thread.ID)
	}

	var requestedBy string
	if err := b.db.QueryRowContext(ctx, `
		SELECT t.created_by
		FROM tasks t
		JOIN agent_runs r ON r.task_id = t.id
		WHERE t.id = $1 AND r.id = $2 AND t.deleted_at IS NULL
	`, ownership.TaskID, ownership.AgentRunID).Scan(&requestedBy); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("runtime ownership task/run not found")
		}
		return fmt.Errorf("load runtime approval owner: %w", err)
	}

	approvalID := uuid.NewSHA1(
		uuid.NameSpaceURL,
		[]byte("dev-plane:agent-runtime:approval:"+thread.ID+":"+event.TurnID+":"+event.Item.ID),
	).String()
	now := b.now()
	metadata, err := json.Marshal(map[string]any{
		"source":       "agent_runtime",
		"provider":     thread.Provider,
		"thread_id":    thread.ID,
		"turn_id":      event.TurnID,
		"item_id":      event.Item.ID,
		"item_name":    event.Item.Name,
		"item_payload": json.RawMessage(event.Item.Payload),
	})
	if err != nil {
		return fmt.Errorf("marshal runtime approval metadata: %w", err)
	}

	result, err := b.db.ExecContext(ctx, `
		INSERT INTO approvals (
			id, task_id, agent_run_id, approval_type, requested_by,
			requested_at, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $6, $6)
		ON CONFLICT(id) DO NOTHING
	`, approvalID, ownership.TaskID, ownership.AgentRunID, models.ApprovalTypeAgentRuntime, requestedBy, now, string(metadata))
	if err != nil {
		return fmt.Errorf("insert runtime approval: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect runtime approval insert: %w", err)
	}
	if rows == 0 {
		return nil
	}

	if _, err := b.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'paused', error_message = $1, updated_at = $2
		WHERE id = $3 AND task_id = $4
	`, "waiting for external agent approval", now, ownership.AgentRunID, ownership.TaskID); err != nil {
		return fmt.Errorf("pause runtime agent run: %w", err)
	}

	if b.publisher != nil {
		payload, err := json.Marshal(map[string]any{
			"approval_id":   approvalID,
			"task_id":       ownership.TaskID,
			"agent_run_id":  ownership.AgentRunID,
			"approval_type": models.ApprovalTypeAgentRuntime,
			"thread_id":     thread.ID,
			"turn_id":       event.TurnID,
			"item_id":       event.Item.ID,
			"timestamp":     now,
		})
		if err != nil {
			return fmt.Errorf("marshal runtime approval event: %w", err)
		}
		if err := b.publisher.Publish(events.ApprovalRequested, payload); err != nil {
			return fmt.Errorf("publish runtime approval request: %w", err)
		}
	}
	return nil
}

var _ agentruntime.EventSink = (*Bridge)(nil)
