package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

// TaskCapsuleRecord is the persistence representation of an execution capsule.
// Payload remains opaque to the database package so scheduler schema evolution
// does not couple the storage layer to a specific capsule implementation.
type TaskCapsuleRecord struct {
	AgentRunID string
	TaskID string
	WorkspaceID string
	Version int
	AgentID string
	AgentRole string
	Payload json.RawMessage
}

// TaskLeaseRecord is a queryable ownership claim persisted with a task capsule.
type TaskLeaseRecord struct {
	Path string
	Mode string
}

// UpsertTaskCapsule atomically persists a capsule and replaces its complete
// lease set. Repeating the operation for the same agent run is idempotent.
func (db *DB) UpsertTaskCapsule(ctx context.Context, record TaskCapsuleRecord, leases []TaskLeaseRecord) error {
	normalized, err := validateTaskCapsuleWrite(record, leases)
	if err != nil {
		return err
	}

	return db.WithTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		p1 := db.Placeholder(1)
		p2 := db.Placeholder(2)
		p3 := db.Placeholder(3)
		p4 := db.Placeholder(4)
		p5 := db.Placeholder(5)
		p6 := db.Placeholder(6)
		p7 := db.Placeholder(7)

		_, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO task_capsules (
				agent_run_id, task_id, workspace_id, version,
				agent_id, agent_role, payload, created_at, updated_at
			) VALUES (%s, %s, %s, %s, %s, %s, %s, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			ON CONFLICT(agent_run_id) DO UPDATE SET
				task_id = excluded.task_id,
				workspace_id = excluded.workspace_id,
				version = excluded.version,
				agent_id = excluded.agent_id,
				agent_role = excluded.agent_role,
				payload = excluded.payload,
				updated_at = CURRENT_TIMESTAMP
		`, p1, p2, p3, p4, p5, p6, p7),
			record.AgentRunID,
			record.TaskID,
			record.WorkspaceID,
			record.Version,
			record.AgentID,
			record.AgentRole,
			string(record.Payload),
		)
		if err != nil {
			return fmt.Errorf("upsert task capsule: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM task_leases WHERE agent_run_id = %s", db.Placeholder(1)),
			record.AgentRunID,
		); err != nil {
			return fmt.Errorf("replace task capsule leases: %w", err)
		}

		for _, lease := range normalized {
			_, err := tx.ExecContext(ctx, fmt.Sprintf(`
				INSERT INTO task_leases (
					agent_run_id, path, mode, acquired_at, updated_at
				) VALUES (%s, %s, %s, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
			`, db.Placeholder(1), db.Placeholder(2), db.Placeholder(3)),
				record.AgentRunID,
				lease.Path,
				lease.Mode,
			)
			if err != nil {
				return fmt.Errorf("insert task lease %s: %w", lease.Path, err)
			}
		}

		return nil
	})
}

// LoadTaskCapsule returns the stored capsule and its complete lease set.
func (db *DB) LoadTaskCapsule(ctx context.Context, agentRunID string) (TaskCapsuleRecord, []TaskLeaseRecord, error) {
	agentRunID = strings.TrimSpace(agentRunID)
	if agentRunID == "" {
		return TaskCapsuleRecord{}, nil, errors.New("agent run id is required")
	}

	var record TaskCapsuleRecord
	var payload []byte
	err := db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT agent_run_id, task_id, workspace_id, version, agent_id, agent_role, payload
		FROM task_capsules
		WHERE agent_run_id = %s
	`, db.Placeholder(1)), agentRunID).Scan(
		&record.AgentRunID,
		&record.TaskID,
		&record.WorkspaceID,
		&record.Version,
		&record.AgentID,
		&record.AgentRole,
		&payload,
	)
	if err != nil {
		return TaskCapsuleRecord{}, nil, fmt.Errorf("load task capsule: %w", err)
	}
	record.Payload = append(json.RawMessage(nil), payload...)

	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT path, mode
		FROM task_leases
		WHERE agent_run_id = %s
		ORDER BY path
	`, db.Placeholder(1)), agentRunID)
	if err != nil {
		return TaskCapsuleRecord{}, nil, fmt.Errorf("load task leases: %w", err)
	}
	defer rows.Close()

	var leases []TaskLeaseRecord
	for rows.Next() {
		var lease TaskLeaseRecord
		if err := rows.Scan(&lease.Path, &lease.Mode); err != nil {
			return TaskCapsuleRecord{}, nil, fmt.Errorf("scan task lease: %w", err)
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return TaskCapsuleRecord{}, nil, fmt.Errorf("iterate task leases: %w", err)
	}
	return record, leases, nil
}

func validateTaskCapsuleWrite(record TaskCapsuleRecord, leases []TaskLeaseRecord) ([]TaskLeaseRecord, error) {
	if strings.TrimSpace(record.AgentRunID) == "" {
		return nil, errors.New("agent run id is required")
	}
	if strings.TrimSpace(record.TaskID) == "" {
		return nil, errors.New("task id is required")
	}
	if strings.TrimSpace(record.WorkspaceID) == "" {
		return nil, errors.New("workspace id is required")
	}
	if record.Version <= 0 {
		return nil, errors.New("capsule version must be positive")
	}
	if strings.TrimSpace(record.AgentID) == "" {
		return nil, errors.New("agent id is required")
	}
	if strings.TrimSpace(record.AgentRole) == "" {
		return nil, errors.New("agent role is required")
	}
	if len(record.Payload) == 0 || !json.Valid(record.Payload) {
		return nil, errors.New("capsule payload must be valid JSON")
	}

	normalized := make([]TaskLeaseRecord, 0, len(leases))
	seen := make(map[string]struct{}, len(leases))
	for i, lease := range leases {
		leasePath, err := normalizeTaskLeasePath(lease.Path)
		if err != nil {
			return nil, fmt.Errorf("lease at index %d: %w", i, err)
		}
		mode := strings.TrimSpace(lease.Mode)
		if mode != "exclusive" {
			return nil, fmt.Errorf("lease %s has unsupported mode %q", leasePath, lease.Mode)
		}
		if _, exists := seen[leasePath]; exists {
			return nil, fmt.Errorf("duplicate lease path %q", leasePath)
		}
		seen[leasePath] = struct{}{}
		normalized = append(normalized, TaskLeaseRecord{Path: leasePath, Mode: mode})
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Path < normalized[j].Path
	})
	return normalized, nil
}

func normalizeTaskLeasePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimSuffix(value, "/**")
	value = strings.TrimSuffix(value, "/*")
	value = strings.TrimSuffix(value, "/")
	if value == "" || strings.HasPrefix(value, "/") {
		return "", errors.New("lease path must be repository-relative")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("lease path escapes repository root")
	}
	return cleaned, nil
}
