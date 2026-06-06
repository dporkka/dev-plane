package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/events"
)

// Approval represents an approval request record.
type Approval struct {
	ID           string          `json:"id"`
	TaskID       string          `json:"task_id"`
	AgentRunID   *string         `json:"agent_run_id,omitempty"`
	ApprovalType string          `json:"approval_type"`
	RequestedBy  string          `json:"requested_by"`
	RequestedAt  time.Time       `json:"requested_at"`
	RespondedBy  *string         `json:"responded_by,omitempty"`
	Response     *string         `json:"response,omitempty"`
	ResponseNote *string         `json:"response_note,omitempty"`
	RespondedAt  *time.Time      `json:"responded_at,omitempty"`
	ExpiresAt    *time.Time      `json:"expires_at,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

// RespondApprovalRequest is the request body for responding to an approval.
type RespondApprovalRequest struct {
	Response     string `json:"response"` // approved, rejected
	ResponseNote string `json:"response_note,omitempty"`
}

// ListApprovals returns all approvals for a task.
func (h *Handler) ListApprovals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	taskID := chi.URLParam(r, "taskID")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id, task_id, agent_run_id, approval_type, requested_by, requested_at,
		       responded_by, response, response_note, responded_at, expires_at, metadata
		FROM approvals
		WHERE task_id = $1
		ORDER BY requested_at DESC
	`, taskID)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()

	var approvals []Approval
	for rows.Next() {
		var a Approval
		var agentRunID, respondedBy, response, responseNote sql.NullString
		var respondedAt, expiresAt sql.NullTime
		var metadata sql.NullString

		err := rows.Scan(
			&a.ID, &a.TaskID, &agentRunID, &a.ApprovalType, &a.RequestedBy, &a.RequestedAt,
			&respondedBy, &response, &responseNote, &respondedAt, &expiresAt, &metadata,
		)
		if err != nil {
			respond.Error(w, http.StatusInternalServerError, err)
			return
		}
		if agentRunID.Valid {
			a.AgentRunID = &agentRunID.String
		}
		if respondedBy.Valid {
			a.RespondedBy = &respondedBy.String
		}
		if response.Valid {
			a.Response = &response.String
		}
		if responseNote.Valid {
			a.ResponseNote = &responseNote.String
		}
		if respondedAt.Valid {
			a.RespondedAt = &respondedAt.Time
		}
		if expiresAt.Valid {
			a.ExpiresAt = &expiresAt.Time
		}
		if metadata.Valid {
			a.Metadata = json.RawMessage(metadata.String)
		}
		approvals = append(approvals, a)
	}

	if approvals == nil {
		approvals = []Approval{}
	}
	respond.JSON(w, http.StatusOK, approvals)
}

// RespondApproval processes an approval response (approved or rejected).
func (h *Handler) RespondApproval(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if id == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("approval id is required"))
		return
	}

	user := auth.UserFromContext(ctx)
	if user == nil {
		respond.Error(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	var req RespondApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	if req.Response != "approved" && req.Response != "rejected" {
		respond.Error(w, http.StatusBadRequest, errors.New("response must be 'approved' or 'rejected'"))
		return
	}

	var approval struct {
		TaskID       string
		AgentRunID   *string
		ApprovalType string
	}
	var agentRunID sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT task_id, agent_run_id, approval_type
		FROM approvals
		WHERE id = $1
	`, id).Scan(&approval.TaskID, &agentRunID, &approval.ApprovalType)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("approval not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if agentRunID.Valid {
		approval.AgentRunID = &agentRunID.String
	}

	now := time.Now().UTC()
	result, err := h.db.ExecContext(ctx, `
		UPDATE approvals
		SET responded_by = $1, response = $2, response_note = $3, responded_at = $4, updated_at = $4
		WHERE id = $5 AND responded_at IS NULL
	`, user.UserID, req.Response, req.ResponseNote, now, id)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		respond.Error(w, http.StatusNotFound, errors.New("approval not found or already responded"))
		return
	}

	if req.Response == "rejected" {
		if _, err := h.db.ExecContext(ctx, `
			UPDATE tasks SET status = 'failed', updated_at = $1
			WHERE id = $2 AND deleted_at IS NULL
		`, now, approval.TaskID); err != nil {
			h.logger.Warn("failed to update task status on approval rejection", "error", err)
		}
	}

	if h.eventBus != nil {
		event := map[string]interface{}{
			"approval_id":   id,
			"task_id":       approval.TaskID,
			"agent_run_id":  approval.AgentRunID,
			"response":      req.Response,
			"responder_id":  user.UserID,
			"approval_type": approval.ApprovalType,
			"note":          req.ResponseNote,
		}
		data, _ := json.Marshal(event)
		subject := events.ApprovalRejected
		if req.Response == "approved" {
			subject = events.ApprovalApproved
		}
		if pubErr := h.eventBus.Publish(subject, data); pubErr != nil {
			h.logger.Warn("failed to publish approval response event", "subject", subject, "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusOK, map[string]string{
		"status":   "responded",
		"response": req.Response,
	})
}
