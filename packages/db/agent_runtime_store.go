package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

// PutThread inserts or updates a durable provider-neutral agent thread.
func (db *DB) PutThread(ctx context.Context, thread agentruntime.Thread) error {
	if err := thread.Validate(); err != nil {
		return fmt.Errorf("validate agent thread: %w", err)
	}
	payload, err := json.Marshal(thread)
	if err != nil {
		return fmt.Errorf("marshal agent thread %s: %w", thread.ID, err)
	}

	query := `
		INSERT INTO agent_threads (
			id, workspace_id, provider, provider_thread_id, model, status, payload, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			workspace_id = excluded.workspace_id,
			provider = excluded.provider,
			provider_thread_id = excluded.provider_thread_id,
			model = excluded.model,
			status = excluded.status,
			payload = excluded.payload,
			updated_at = CURRENT_TIMESTAMP
	`
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	_, err = db.ExecContext(
		ctx,
		query,
		thread.ID,
		thread.WorkspaceID,
		thread.Provider,
		nullableString(thread.ProviderThreadID),
		nullableString(thread.Model),
		string(thread.Status),
		payload,
	)
	if err != nil {
		return fmt.Errorf("put agent thread %s: %w", thread.ID, err)
	}
	return nil
}

// GetThread returns a durable provider-neutral agent thread.
func (db *DB) GetThread(ctx context.Context, id string) (agentruntime.Thread, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return agentruntime.Thread{}, errors.New("agent thread id is required")
	}
	query := "SELECT payload FROM agent_threads WHERE id = ?"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		return agentruntime.Thread{}, fmt.Errorf("get agent thread %s: %w", id, err)
	}
	var thread agentruntime.Thread
	if err := json.Unmarshal(raw, &thread); err != nil {
		return agentruntime.Thread{}, fmt.Errorf("decode agent thread %s: %w", id, err)
	}
	return thread, nil
}

// PutTurn inserts or updates a durable turn.
func (db *DB) PutTurn(ctx context.Context, turn agentruntime.Turn) error {
	if err := turn.Validate(); err != nil {
		return fmt.Errorf("validate agent turn: %w", err)
	}
	payload, err := json.Marshal(turn)
	if err != nil {
		return fmt.Errorf("marshal agent turn %s: %w", turn.ID, err)
	}

	query := `
		INSERT INTO agent_turns (
			id, thread_id, provider_turn_id, status, payload, started_at, completed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = excluded.thread_id,
			provider_turn_id = excluded.provider_turn_id,
			status = excluded.status,
			payload = excluded.payload,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			updated_at = CURRENT_TIMESTAMP
	`
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	_, err = db.ExecContext(
		ctx,
		query,
		turn.ID,
		turn.ThreadID,
		nullableString(turn.ProviderTurnID),
		string(turn.Status),
		payload,
		turn.StartedAt,
		turn.CompletedAt,
	)
	if err != nil {
		return fmt.Errorf("put agent turn %s: %w", turn.ID, err)
	}
	return nil
}

// GetTurn returns a durable turn by id.
func (db *DB) GetTurn(ctx context.Context, id string) (agentruntime.Turn, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return agentruntime.Turn{}, errors.New("agent turn id is required")
	}
	query := "SELECT payload FROM agent_turns WHERE id = ?"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		return agentruntime.Turn{}, fmt.Errorf("get agent turn %s: %w", id, err)
	}
	var turn agentruntime.Turn
	if err := json.Unmarshal(raw, &turn); err != nil {
		return agentruntime.Turn{}, fmt.Errorf("decode agent turn %s: %w", id, err)
	}
	return turn, nil
}

// ListTurns returns turns for a thread in durable creation order.
func (db *DB) ListTurns(ctx context.Context, threadID string) ([]agentruntime.Turn, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, errors.New("agent thread id is required")
	}
	query := "SELECT payload FROM agent_turns WHERE thread_id = ? ORDER BY created_at, id"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	rows, err := db.QueryContext(ctx, query, threadID)
	if err != nil {
		return nil, fmt.Errorf("list agent turns for %s: %w", threadID, err)
	}
	defer rows.Close()

	var turns []agentruntime.Turn
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan agent turn for %s: %w", threadID, err)
		}
		var turn agentruntime.Turn
		if err := json.Unmarshal(raw, &turn); err != nil {
			return nil, fmt.Errorf("decode agent turn for %s: %w", threadID, err)
		}
		turns = append(turns, turn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent turns for %s: %w", threadID, err)
	}
	return turns, nil
}

// PutItem inserts or updates a durable turn item.
func (db *DB) PutItem(ctx context.Context, item agentruntime.Item) error {
	if err := item.Validate(); err != nil {
		return fmt.Errorf("validate agent item: %w", err)
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal agent item %s: %w", item.ID, err)
	}

	query := `
		INSERT INTO agent_items (
			id, thread_id, turn_id, provider_item_id, item_type, status, payload, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			thread_id = excluded.thread_id,
			turn_id = excluded.turn_id,
			provider_item_id = excluded.provider_item_id,
			item_type = excluded.item_type,
			status = excluded.status,
			payload = excluded.payload,
			updated_at = CURRENT_TIMESTAMP
	`
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	_, err = db.ExecContext(
		ctx,
		query,
		item.ID,
		item.ThreadID,
		item.TurnID,
		nullableString(item.ProviderItemID),
		string(item.Type),
		string(item.Status),
		payload,
	)
	if err != nil {
		return fmt.Errorf("put agent item %s: %w", item.ID, err)
	}
	return nil
}

// GetItem returns a durable turn item by id.
func (db *DB) GetItem(ctx context.Context, id string) (agentruntime.Item, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return agentruntime.Item{}, errors.New("agent item id is required")
	}
	query := "SELECT payload FROM agent_items WHERE id = ?"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	var raw []byte
	if err := db.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		return agentruntime.Item{}, fmt.Errorf("get agent item %s: %w", id, err)
	}
	var item agentruntime.Item
	if err := json.Unmarshal(raw, &item); err != nil {
		return agentruntime.Item{}, fmt.Errorf("decode agent item %s: %w", id, err)
	}
	return item, nil
}

// ListItems returns items for a turn in durable creation order.
func (db *DB) ListItems(ctx context.Context, turnID string) ([]agentruntime.Item, error) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil, errors.New("agent turn id is required")
	}
	query := "SELECT payload FROM agent_items WHERE turn_id = ? ORDER BY created_at, id"
	if db.Driver == "postgres" {
		query = rebindPostgres(query)
	}
	rows, err := db.QueryContext(ctx, query, turnID)
	if err != nil {
		return nil, fmt.Errorf("list agent items for %s: %w", turnID, err)
	}
	defer rows.Close()

	var items []agentruntime.Item
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan agent item for %s: %w", turnID, err)
		}
		var item agentruntime.Item
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode agent item for %s: %w", turnID, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agent items for %s: %w", turnID, err)
	}
	return items, nil
}

var _ agentruntime.Store = (*DB)(nil)
