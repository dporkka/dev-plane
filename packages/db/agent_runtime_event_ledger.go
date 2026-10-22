package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

// AppendRuntimeEvent stores one immutable provider-neutral runtime event. The
// (thread_id, turn_id, sequence) primary key makes repeated delivery idempotent
// without mutating previously recorded history.
func (db *DB) AppendRuntimeEvent(ctx context.Context, record agentruntime.RuntimeEventRecord) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("validate runtime event record: %w", err)
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal runtime event record: %w", err)
	}

	query := `
		INSERT INTO agent_runtime_events (
			workspace_id, thread_id, turn_id, provider, sequence,
			event_type, event_record, occurred_at, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(thread_id, turn_id, sequence) DO NOTHING
	`
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	if _, err := db.ExecContext(
		ctx,
		query,
		record.WorkspaceID,
		record.ThreadID,
		record.TurnID,
		record.Provider,
		record.Sequence,
		string(record.Type),
		payload,
		record.OccurredAt,
		record.RecordedAt,
	); err != nil {
		return fmt.Errorf("append runtime event %s/%s/%d: %w", record.ThreadID, record.TurnID, record.Sequence, err)
	}
	return nil
}

// ListRuntimeEvents returns immutable runtime history in occurrence order. When
// turnID is empty, events for the entire thread are returned.
func (db *DB) ListRuntimeEvents(ctx context.Context, threadID, turnID string) ([]agentruntime.RuntimeEventRecord, error) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" {
		return nil, errors.New("agent runtime event thread id is required")
	}

	query := `
		SELECT event_record
		FROM agent_runtime_events
		WHERE thread_id = ?
		ORDER BY occurred_at, turn_id, sequence
	`
	args := []any{threadID}
	if turnID != "" {
		query = `
			SELECT event_record
			FROM agent_runtime_events
			WHERE thread_id = ? AND turn_id = ?
			ORDER BY sequence
		`
		args = append(args, turnID)
	}
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list runtime events for thread %s: %w", threadID, err)
	}
	defer rows.Close()

	var records []agentruntime.RuntimeEventRecord
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan runtime event for thread %s: %w", threadID, err)
		}
		var record agentruntime.RuntimeEventRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, fmt.Errorf("decode runtime event for thread %s: %w", threadID, err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate runtime events for thread %s: %w", threadID, err)
	}
	return records, nil
}

var _ agentruntime.RuntimeEventLedger = (*DB)(nil)
