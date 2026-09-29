package forgereplay

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

type State string

const (
	StateNew       State = "new"
	StateInFlight  State = "in_flight"
	StateReplay    State = "replay"
	StateUncertain State = "uncertain"
)

var (
	ErrConflict       = errors.New("forge request id conflicts with an existing request")
	ErrInvalidRequest = errors.New("invalid forge replay request")
	ErrNotFound       = errors.New("forge replay request not found")
)

type Request struct {
	ID         string
	WorkloadID string
	TaskID     string
	RunID      string
	Operation  string
	Body       []byte
}

type Result struct {
	State          State
	ResponseStatus int
	ResponseBody   []byte
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Claim(ctx context.Context, req Request) (Result, error) {
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	bodyHash := hashBody(req.Body)

	result, err := s.db.ExecContext(ctx, `
		INSERT INTO forge_workload_requests (
			request_id, workload_id, body_sha256, task_id, run_id, operation, state
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		ON CONFLICT(request_id) DO NOTHING
	`, req.ID, req.WorkloadID, bodyHash, req.TaskID, req.RunID, req.Operation)
	if err != nil {
		return Result{}, fmt.Errorf("claim forge request: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return Result{}, fmt.Errorf("claim forge request rows affected: %w", err)
	}
	if rows == 1 {
		return Result{State: StateNew}, nil
	}

	var (
		workloadID     string
		existingHash   string
		taskID         string
		runID          string
		operation      string
		state          string
		responseStatus sql.NullInt64
		responseBody   sql.NullString
	)
	err = s.db.QueryRowContext(ctx, `
		SELECT workload_id, body_sha256, task_id, run_id, operation, state,
		       response_status, response_body
		FROM forge_workload_requests
		WHERE request_id = $1
	`, req.ID).Scan(
		&workloadID,
		&existingHash,
		&taskID,
		&runID,
		&operation,
		&state,
		&responseStatus,
		&responseBody,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, ErrNotFound
		}
		return Result{}, fmt.Errorf("load existing forge request: %w", err)
	}

	if workloadID != req.WorkloadID ||
		existingHash != bodyHash ||
		taskID != req.TaskID ||
		runID != req.RunID ||
		operation != req.Operation {
		return Result{}, ErrConflict
	}

	switch state {
	case "pending":
		return Result{State: StateInFlight}, nil
	case "completed":
		out := Result{State: StateReplay}
		if responseStatus.Valid {
			out.ResponseStatus = int(responseStatus.Int64)
		}
		if responseBody.Valid {
			out.ResponseBody = []byte(responseBody.String)
		}
		return out, nil
	case "uncertain":
		out := Result{State: StateUncertain}
		if responseBody.Valid {
			out.ResponseBody = []byte(responseBody.String)
		}
		return out, nil
	default:
		return Result{}, fmt.Errorf("unknown forge request state %q", state)
	}
}

func (s *Store) Complete(ctx context.Context, requestID string, status int, body []byte) error {
	if !validRequestID(requestID) {
		return ErrInvalidRequest
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE forge_workload_requests
		SET state = 'completed',
		    response_status = $1,
		    response_body = $2,
		    completed_at = CURRENT_TIMESTAMP
		WHERE request_id = $3 AND state = 'pending'
	`, status, string(body), requestID)
	if err != nil {
		return fmt.Errorf("complete forge request: %w", err)
	}
	return requireOneRow(result, "complete forge request")
}

func (s *Store) MarkUncertain(ctx context.Context, requestID string, detail []byte) error {
	if !validRequestID(requestID) {
		return ErrInvalidRequest
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE forge_workload_requests
		SET state = 'uncertain',
		    response_body = $1,
		    completed_at = CURRENT_TIMESTAMP
		WHERE request_id = $2 AND state = 'pending'
	`, string(detail), requestID)
	if err != nil {
		return fmt.Errorf("mark forge request uncertain: %w", err)
	}
	return requireOneRow(result, "mark forge request uncertain")
}

func requireOneRow(result sql.Result, action string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", action, err)
	}
	if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func validateRequest(req Request) error {
	if !validRequestID(req.ID) {
		return fmt.Errorf("%w: request id must be 16-128 safe ASCII characters", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.WorkloadID) == "" ||
		strings.TrimSpace(req.TaskID) == "" ||
		strings.TrimSpace(req.RunID) == "" ||
		strings.TrimSpace(req.Operation) == "" {
		return fmt.Errorf("%w: workload, task, run, and operation are required", ErrInvalidRequest)
	}
	return nil
}

func validRequestID(id string) bool {
	if len(id) < 16 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func hashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
