// Package approvals manages approval requests and responses.
//
// The Service creates approval requests, processes responses, and integrates
// with the capability kernel to check if approvals are required for specific
// actions. It publishes events when approvals are granted or rejected.
package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

// Service manages approval requests and responses.
type Service struct {
	db       *sql.DB
	kernel   *capability.Kernel
	eventBus *events.Bus
	logger   *slog.Logger
}

// NewService creates a new approval service.
func NewService(db *sql.DB, kernel *capability.Kernel, eventBus *events.Bus, logger *slog.Logger) *Service {
	return &Service{
		db:       db,
		kernel:   kernel,
		eventBus: eventBus,
		logger:   logger,
	}
}

// RequestApproval creates a new approval request.
func (s *Service) RequestApproval(ctx context.Context, taskID, approvalType, requestedBy string, details map[string]any) (*models.Approval, error) {
	// Validate the task exists
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE id = $1 AND deleted_at IS NULL)`, taskID).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("check task existence: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	approval := &models.Approval{
		ID:           uuid.New().String(),
		TaskID:       taskID,
		ApprovalType: approvalType,
		RequestedBy:  requestedBy,
		RequestedAt:  time.Now().UTC(),
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if details != nil {
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			return nil, fmt.Errorf("marshal approval details: %w", err)
		}
		approval.Metadata = detailsJSON
	}

	if err := approval.Validate(); err != nil {
		return nil, fmt.Errorf("validate approval: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO approvals (
			id, task_id, approval_type, requested_by, requested_at,
			metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
	`, approval.ID, approval.TaskID, approval.ApprovalType, approval.RequestedBy,
		approval.RequestedAt, approval.Metadata, approval.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert approval: %w", err)
	}

	s.logger.Info("approval requested",
		"approval_id", approval.ID,
		"task_id", taskID,
		"type", approvalType,
		"requested_by", requestedBy,
	)

	return approval, nil
}

// RespondApproval processes an approval response (approve/reject).
//
//  1. Validate the approval exists and is pending
//  2. Check the responder has permission (via capability kernel)
//  3. Update approval status
//  4. If approved, publish approval.approved event
//  5. If rejected, publish approval.rejected event, update task status
func (s *Service) RespondApproval(ctx context.Context, approvalID, responderID, response, note string) error {
	if response != models.ApprovalResponseApproved && response != models.ApprovalResponseRejected {
		return fmt.Errorf("invalid response %q, must be %q or %q", response, models.ApprovalResponseApproved, models.ApprovalResponseRejected)
	}

	// 1. Get approval and verify it's pending
	approval, err := s.getApproval(ctx, approvalID)
	if err != nil {
		return err
	}
	if !approval.IsPending() {
		return fmt.Errorf("approval %s is not pending (already responded)", approvalID)
	}
	if approval.IsExpired() {
		return fmt.Errorf("approval %s has expired", approvalID)
	}

	// 2. Check permission via capability kernel (if available)
	if s.kernel != nil {
		// Load the task for context
		var task models.Task
		var desc, sourceID, wsID, spec, ac, maxCost, approvalReqs, metadata sql.NullString
		var startedAt, completedAt sql.NullTime
		err := s.db.QueryRowContext(ctx, `
			SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id,
			       title, description, status, priority, risk_level, target_branch,
			       spec, acceptance_criteria, max_cost, max_runtime_minutes,
			       approval_requirements, metadata, started_at, completed_at, created_at, updated_at
			FROM tasks WHERE id = $1 AND deleted_at IS NULL
		`, approval.TaskID).Scan(
			&task.ID, &task.ProjectID, &task.RepositoryID, &wsID, &task.CreatedBy, &task.Source, &sourceID,
			&task.Title, &desc, &task.Status, &task.Priority, &task.RiskLevel, &task.TargetBranch,
			&spec, &ac, &maxCost, &task.MaxRuntimeMinutes,
			&approvalReqs, &metadata, &startedAt, &completedAt, &task.CreatedAt, &task.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("load task for permission check: %w", err)
		}
		if desc.Valid {
			task.Description = &desc.String
		}

		// Build capability request
		op := capability.OpOpenPR
		if approval.ApprovalType == models.ApprovalTypeDeploy {
			op = capability.OpDeploy
		} else if approval.ApprovalType == models.ApprovalTypePRCreate {
			op = capability.OpOpenPR
		} else if approval.ApprovalType == models.ApprovalTypeExecution {
			op = capability.OpRunCommand
		}

		req := capability.Request{
			ActorType: "human",
			Task:      &task,
			Operation: op,
		}
		if approval.AgentRunID != nil {
			req.AgentRun = &models.AgentRun{ID: *approval.AgentRunID}
		}

		result, err := s.kernel.Evaluate(ctx, req)
		if err != nil {
			s.logger.Warn("capability kernel evaluation failed, allowing response", "error", err)
		} else if result.Effect == policies.EffectDeny {
			return fmt.Errorf("responder does not have permission to approve this action: %s", result.Reason)
		}
	}

	// 3. Update approval
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `
		UPDATE approvals SET
			responded_by = $1,
			response = $2,
			response_note = $3,
			responded_at = $4,
			updated_at = $4
		WHERE id = $5
	`, responderID, response, note, now, approvalID)
	if err != nil {
		return fmt.Errorf("update approval: %w", err)
	}

	s.logger.Info("approval responded",
		"approval_id", approvalID,
		"response", response,
		"responder_id", responderID,
		"task_id", approval.TaskID,
	)

	// 4. Publish event
	if s.eventBus != nil {
		event := map[string]interface{}{
			"approval_id":   approvalID,
			"task_id":       approval.TaskID,
			"agent_run_id":  approval.AgentRunID,
			"response":      response,
			"responder_id":  responderID,
			"approval_type": approval.ApprovalType,
			"note":          note,
		}
		data, _ := json.Marshal(event)
		if response == models.ApprovalResponseApproved {
			if pubErr := s.eventBus.Publish("approval.approved", data); pubErr != nil {
				s.logger.Warn("failed to publish approval.approved event", "error", pubErr)
			}
		} else {
			if pubErr := s.eventBus.Publish("approval.rejected", data); pubErr != nil {
				s.logger.Warn("failed to publish approval.rejected event", "error", pubErr)
			}
			// Update task status on rejection
			_, dbErr := s.db.ExecContext(ctx, `
				UPDATE tasks SET status = 'failed', updated_at = $1
				WHERE id = $2
			`, now, approval.TaskID)
			if dbErr != nil {
				s.logger.Warn("failed to update task status on rejection", "error", dbErr)
			}
		}
	}

	return nil
}

// GetPendingApprovals returns pending approvals for a task.
func (s *Service) GetPendingApprovals(ctx context.Context, taskID string) ([]models.Approval, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, agent_run_id, approval_type, requested_by, requested_at,
		       responded_by, response, response_note, responded_at, expires_at,
		       metadata, created_at, updated_at
		FROM approvals
		WHERE task_id = $1 AND response IS NULL
		  AND (expires_at IS NULL OR expires_at > $2)
		ORDER BY requested_at DESC
	`, taskID, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("query pending approvals: %w", err)
	}
	defer rows.Close()

	return scanApprovals(rows)
}

// IsApprovalRequired checks if a task requires approval for a given action.
func (s *Service) IsApprovalRequired(ctx context.Context, taskID, action string) (bool, error) {
	return s.checkApproval(ctx, taskID, action)
}

// checkApproval checks capability kernel for approval requirement.
func (s *Service) checkApproval(ctx context.Context, taskID, action string) (bool, error) {
	// Load task
	var task models.Task
	err := s.db.QueryRowContext(ctx, `
		SELECT id, project_id, status, risk_level, target_branch
		FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&task.ID, &task.ProjectID, &task.Status, &task.RiskLevel, &task.TargetBranch)
	if err != nil {
		return false, fmt.Errorf("load task: %w", err)
	}

	// Check if task has explicit approval requirements
	var approvalReqs json.RawMessage
	err = s.db.QueryRowContext(ctx, `
		SELECT approval_requirements FROM tasks WHERE id = $1
	`, taskID).Scan(&approvalReqs)
	if err == nil && len(approvalReqs) > 0 {
		var reqs []struct {
			Action string `json:"action"`
			Type   string `json:"type"`
		}
		if jsonErr := json.Unmarshal(approvalReqs, &reqs); jsonErr == nil {
			for _, req := range reqs {
				if req.Action == action || req.Action == "*" {
					return true, nil
				}
			}
		}
	}

	// Use capability kernel if available
	if s.kernel != nil {
		// Determine operation from action
		op := mapActionToOperation(action)

		req := capability.Request{
			ActorType: "agent",
			Task:      &task,
			Operation: op,
		}

		result, err := s.kernel.Evaluate(ctx, req)
		if err != nil {
			s.logger.Warn("capability check failed, defaulting to approval required", "error", err)
			return true, nil // Default to requiring approval on error
		}
		return result.RequiredApproval, nil
	}

	// Fallback: require approval for high-risk actions
	highRiskActions := map[string]bool{
		"create_pr":   true,
		"deploy":      true,
		"merge":       true,
		"push":        true,
		"migrate":     true,
		"destructive": true,
	}
	return highRiskActions[action], nil
}

// getApproval retrieves a single approval by ID.
func (s *Service) getApproval(ctx context.Context, approvalID string) (*models.Approval, error) {
	var a models.Approval
	var agentRunID, respondedBy, response, responseNote sql.NullString
	var respondedAt, expiresAt sql.NullTime
	var metadata sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, agent_run_id, approval_type, requested_by, requested_at,
		       responded_by, response, response_note, responded_at, expires_at,
		       metadata, created_at, updated_at
		FROM approvals WHERE id = $1
	`, approvalID).Scan(
		&a.ID, &a.TaskID, &agentRunID, &a.ApprovalType, &a.RequestedBy, &a.RequestedAt,
		&respondedBy, &response, &responseNote, &respondedAt, &expiresAt,
		&metadata, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("approval %s not found", approvalID)
		}
		return nil, fmt.Errorf("get approval: %w", err)
	}
	if agentRunID.Valid {
		ar := agentRunID.String
		a.AgentRunID = &ar
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

	return &a, nil
}

// scanApprovals scans approval rows into a slice.
func scanApprovals(rows *sql.Rows) ([]models.Approval, error) {
	var approvals []models.Approval
	for rows.Next() {
		var a models.Approval
		var agentRunID, respondedBy, response, responseNote sql.NullString
		var respondedAt, expiresAt sql.NullTime
		var metadata sql.NullString

		err := rows.Scan(
			&a.ID, &a.TaskID, &agentRunID, &a.ApprovalType, &a.RequestedBy, &a.RequestedAt,
			&respondedBy, &response, &responseNote, &respondedAt, &expiresAt,
			&metadata, &a.CreatedAt, &a.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		if agentRunID.Valid {
			ar := agentRunID.String
			a.AgentRunID = &ar
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
		approvals = []models.Approval{}
	}
	return approvals, rows.Err()
}

// mapActionToOperation maps an action string to a capability kernel operation.
func mapActionToOperation(action string) string {
	switch action {
	case "create_pr":
		return capability.OpOpenPR
	case "merge":
		return capability.OpMergePR
	case "deploy":
		return capability.OpDeploy
	case "push":
		return capability.OpPushBranch
	case "commit":
		return capability.OpCreateCommit
	case "migrate":
		return capability.OpRunMigration
	case "destructive_db":
		return capability.OpDestructiveDB
	case "write_file":
		return capability.OpWriteFile
	case "delete_file":
		return capability.OpDeleteFile
	case "access_secret":
		return capability.OpAccessSecret
	default:
		return action
	}
}
