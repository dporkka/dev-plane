package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/models"
)

type forgeApprovalState string

const (
	forgeApprovalPending  forgeApprovalState = "pending"
	forgeApprovalApproved forgeApprovalState = "approved"
	forgeApprovalRejected forgeApprovalState = "rejected"
	forgeApprovalExpired  forgeApprovalState = "expired"
)

type forgeApprovalDecision struct {
	ID    string
	State forgeApprovalState
}

func (h *Handler) ensureForgeApproval(
	ctx context.Context,
	requestID string,
	fctx *forgeContext,
	operation string,
	commandType string,
) (forgeApprovalDecision, error) {
	decision, err := h.loadForgeApproval(ctx, requestID)
	if err == nil {
		return decision, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return forgeApprovalDecision{}, fmt.Errorf("load forge approval: %w", err)
	}

	id := uuid.New().String()
	now := time.Now().UTC()
	metadata, err := json.Marshal(map[string]any{
		"auto_created": true,
		"reason":       "forge_capability",
		"request_id":   requestID,
		"operation":    operation,
		"command_type": commandType,
		"repository":   fctx.Repository.FullName,
	})
	if err != nil {
		return forgeApprovalDecision{}, fmt.Errorf("marshal forge approval metadata: %w", err)
	}
	result, err := h.db.ExecContext(ctx, `
		INSERT INTO approvals (
			id, task_id, agent_run_id, approval_type, requested_by,
			requested_at, metadata, forge_request_id, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $6, $6)
		ON CONFLICT DO NOTHING
	`, id, fctx.Task.ID, fctx.Run.ID, models.ApprovalTypeForgeCapability,
		fctx.Task.CreatedBy, now, string(metadata), requestID)
	if err != nil {
		return forgeApprovalDecision{}, fmt.Errorf("create forge approval: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return forgeApprovalDecision{}, fmt.Errorf("check forge approval insert: %w", err)
	}
	if rows == 1 {
		return forgeApprovalDecision{ID: id, State: forgeApprovalPending}, nil
	}

	decision, err = h.loadForgeApproval(ctx, requestID)
	if err != nil {
		return forgeApprovalDecision{}, fmt.Errorf("load concurrently created forge approval: %w", err)
	}
	return decision, nil
}

func (h *Handler) loadForgeApproval(ctx context.Context, requestID string) (forgeApprovalDecision, error) {
	var (
		id        string
		response  sql.NullString
		expiresAt sql.NullTime
	)
	err := h.db.QueryRowContext(ctx, `
		SELECT id, response, expires_at
		FROM approvals
		WHERE forge_request_id = $1
	`, requestID).Scan(&id, &response, &expiresAt)
	if err != nil {
		return forgeApprovalDecision{}, err
	}
	switch {
	case response.Valid && response.String == models.ApprovalResponseApproved:
		return forgeApprovalDecision{ID: id, State: forgeApprovalApproved}, nil
	case response.Valid && response.String == models.ApprovalResponseRejected:
		return forgeApprovalDecision{ID: id, State: forgeApprovalRejected}, nil
	case response.Valid:
		return forgeApprovalDecision{}, fmt.Errorf("unknown forge approval response %q", response.String)
	case expiresAt.Valid && time.Now().UTC().After(expiresAt.Time):
		return forgeApprovalDecision{ID: id, State: forgeApprovalExpired}, nil
	default:
		return forgeApprovalDecision{ID: id, State: forgeApprovalPending}, nil
	}
}
