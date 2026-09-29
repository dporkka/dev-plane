package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/forgereplay"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/api/internal/workloadauth"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

const maxForgeAuthorizeBody = 64 << 10

type ForgeAuthorizeRequest struct {
	RequestID string `json:"request_id"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id"`
	Operation string `json:"operation"`
}

type ForgeRepositoryResponse struct {
	ID            string `json:"id"`
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

type ForgeAuthorizeResponse struct {
	Allowed          bool                    `json:"allowed"`
	RequiredApproval bool                    `json:"required_approval"`
	Effect           string                  `json:"effect"`
	Reason           string                  `json:"reason"`
	RiskLevel        string                  `json:"risk_level"`
	Repository       ForgeRepositoryResponse `json:"repository"`
}

func (h *Handler) AuthorizeForgeWorkload(w http.ResponseWriter, r *http.Request) {
	if h.workloadVerifier == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("workload authentication is not configured"))
		return
	}
	if h.forgeReplayStore == nil {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("forge request replay store is not configured"))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxForgeAuthorizeBody+1))
	if err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("failed to read request body"))
		return
	}
	if len(body) > maxForgeAuthorizeBody {
		respond.Error(w, http.StatusRequestEntityTooLarge, errors.New("request body is too large"))
		return
	}
	if err := h.workloadVerifier.Verify(r, body); err != nil {
		h.logger.WarnContext(r.Context(), "rejected forge workload request", "error", err)
		respond.Error(w, http.StatusUnauthorized, errors.New("invalid workload authorization"))
		return
	}

	var input ForgeAuthorizeRequest
	if err := json.Unmarshal(body, &input); err != nil {
		respond.Error(w, http.StatusBadRequest, errors.New("invalid JSON request"))
		return
	}
	if input.RequestID == "" || input.TaskID == "" || input.RunID == "" || input.Operation == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("request_id, task_id, run_id, and operation are required"))
		return
	}

	ctx := r.Context()
	claim, err := h.forgeReplayStore.Claim(ctx, forgereplay.Request{
		ID:         input.RequestID,
		WorkloadID: r.Header.Get(workloadauth.HeaderWorkload),
		TaskID:     input.TaskID,
		RunID:      input.RunID,
		Operation:  input.Operation,
		Body:       body,
	})
	if err != nil {
		switch {
		case errors.Is(err, forgereplay.ErrConflict):
			respond.Error(w, http.StatusConflict, errors.New("request_id is already bound to a different forge request"))
		case errors.Is(err, forgereplay.ErrInvalidRequest):
			respond.Error(w, http.StatusBadRequest, err)
		default:
			respond.Error(w, http.StatusInternalServerError, fmt.Errorf("claim forge request: %w", err))
		}
		return
	}

	switch claim.State {
	case forgereplay.StateReplay:
		writeForgeJSONBytes(w, claim.ResponseStatus, claim.ResponseBody)
		return
	case forgereplay.StateInFlight:
		respond.Error(w, http.StatusConflict, errors.New("forge request is already in flight"))
		return
	case forgereplay.StateUncertain:
		respond.Error(w, http.StatusConflict, errors.New("forge request outcome is uncertain and requires reconciliation"))
		return
	case forgereplay.StateNew:
		// Continue with canonical context and policy evaluation.
	default:
		h.releaseForgeClaim(ctx, input.RequestID)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("unknown forge replay state %q", claim.State))
		return
	}

	var (
		taskWorkspaceID sql.NullString
		runWorkspaceID  sql.NullString
		startedAt       sql.NullTime
		completedAt     sql.NullTime
		workspaceID     sql.NullString
		workspaceBranch sql.NullString
		runtimeProvider sql.NullString

		task  models.Task
		run   models.AgentRun
		repo  models.Repository
		orgID string
	)

	err = h.db.QueryRowContext(ctx, `
		SELECT
			t.id, t.project_id, t.repository_id, t.workspace_id, t.risk_level, t.target_branch,
			ar.id, ar.workspace_id, ar.agent_role, ar.status,
			ar.prompt_tokens, ar.completion_tokens, ar.total_cost, ar.started_at, ar.completed_at,
			r.id, r.project_id, r.owner, r.name, r.full_name, r.default_branch,
			p.organization_id,
			w.id, w.branch, w.runtime_provider
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		JOIN repositories r ON r.id = t.repository_id
		JOIN projects p ON p.id = t.project_id
		LEFT JOIN workspaces w ON w.id = COALESCE(ar.workspace_id, t.workspace_id)
		WHERE ar.id = $1
		  AND t.id = $2
		  AND t.deleted_at IS NULL
		  AND r.deleted_at IS NULL
		  AND p.deleted_at IS NULL
	`, input.RunID, input.TaskID).Scan(
		&task.ID, &task.ProjectID, &task.RepositoryID, &taskWorkspaceID, &task.RiskLevel, &task.TargetBranch,
		&run.ID, &runWorkspaceID, &run.AgentRole, &run.Status,
		&run.PromptTokens, &run.CompletionTokens, &run.TotalCost, &startedAt, &completedAt,
		&repo.ID, &repo.ProjectID, &repo.Owner, &repo.Name, &repo.FullName, &repo.DefaultBranch,
		&orgID,
		&workspaceID, &workspaceBranch, &runtimeProvider,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.completeForgeError(w, ctx, input.RequestID, http.StatusNotFound, errors.New("forge task/run context not found"))
			return
		}
		h.releaseForgeClaim(ctx, input.RequestID)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("load forge task/run context: %w", err))
		return
	}

	run.TaskID = task.ID
	if taskWorkspaceID.Valid {
		id := taskWorkspaceID.String
		task.WorkspaceID = &id
	}
	if runWorkspaceID.Valid {
		id := runWorkspaceID.String
		run.WorkspaceID = &id
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}

	project := &models.Project{ID: task.ProjectID, OrganizationID: orgID}
	organization := &models.Organization{ID: orgID}
	var workspace *models.Workspace
	if workspaceID.Valid {
		workspace = &models.Workspace{
			ID:              workspaceID.String,
			RepositoryID:    repo.ID,
			Branch:          workspaceBranch.String,
			RuntimeProvider: runtimeProvider.String,
		}
	}

	result, err := h.kernel().EvaluateForge(ctx, input.Operation, capability.Request{
		ActorType:    "agent",
		AgentRole:    run.AgentRole,
		Organization: organization,
		Project:      project,
		Repository:   &repo,
		Workspace:    workspace,
		Task:         &task,
		AgentRun:     &run,
		Resource:     repo.FullName,
		Details: map[string]any{
			"organization_id": orgID,
			"task_id":         task.ID,
			"run_id":          run.ID,
			"agent_role":      run.AgentRole,
			"request_id":      input.RequestID,
		},
	})
	if err != nil {
		if errors.Is(err, capability.ErrCapabilityUnknown) {
			h.completeForgeError(w, ctx, input.RequestID, http.StatusBadRequest, err)
			return
		}
		h.releaseForgeClaim(ctx, input.RequestID)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("evaluate forge capability: %w", err))
		return
	}

	response := ForgeAuthorizeResponse{
		Allowed:          result.Effect == policies.EffectAllow,
		RequiredApproval: result.RequiredApproval,
		Effect:           result.Effect.String(),
		Reason:           result.Reason,
		RiskLevel:        result.RiskLevel,
		Repository: ForgeRepositoryResponse{
			ID:            repo.ID,
			Owner:         repo.Owner,
			Name:          repo.Name,
			FullName:      repo.FullName,
			DefaultBranch: repo.DefaultBranch,
		},
	}
	h.completeForgeJSON(w, ctx, input.RequestID, http.StatusOK, response)
}

func (h *Handler) completeForgeError(w http.ResponseWriter, ctx context.Context, requestID string, status int, err error) {
	h.completeForgeJSON(w, ctx, requestID, status, map[string]string{"error": err.Error()})
}

func (h *Handler) completeForgeJSON(w http.ResponseWriter, ctx context.Context, requestID string, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		h.releaseForgeClaim(ctx, requestID)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("encode forge response: %w", err))
		return
	}
	if err := h.forgeReplayStore.Complete(ctx, requestID, status, body); err != nil {
		h.releaseForgeClaim(ctx, requestID)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("persist forge request outcome: %w", err))
		return
	}
	writeForgeJSONBytes(w, status, body)
}

func (h *Handler) releaseForgeClaim(ctx context.Context, requestID string) {
	if h.forgeReplayStore == nil {
		return
	}
	if err := h.forgeReplayStore.Release(ctx, requestID); err != nil && !errors.Is(err, forgereplay.ErrNotFound) {
		h.logger.WarnContext(ctx, "failed to release forge request claim", "request_id", requestID, "error", err)
	}
}

func writeForgeJSONBytes(w http.ResponseWriter, status int, body []byte) {
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
