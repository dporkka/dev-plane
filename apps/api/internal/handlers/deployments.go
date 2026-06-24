package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

// DeployTaskRequest is the request body for deploying a task.
type DeployTaskRequest struct {
	Environment string `json:"environment"`
	Ref         string `json:"ref,omitempty"`
}

// DeploymentResponse is the API representation of a deployment.
type DeploymentResponse struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	Environment string    `json:"environment"`
	Ref         string    `json:"ref"`
	Provider    string    `json:"provider"`
	Status      string    `json:"status"`
	URL         string    `json:"url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// DeployTask triggers a deployment for a task after capability authorization.
// It records a deployment record, calls the deployment provider, and transitions
// the task to deploying.
func (h *Handler) DeployTask(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("task id is required"))
		return
	}

	if err := authz.AuthorizeTask(ctx, h.db, user, taskID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("task not found"))
		return
	}

	var req DeployTaskRequest
	if r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Environment == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("environment is required"))
		return
	}

	var task struct {
		Status     string
		RepoID     string
		Branch     string
		ProjectID  string
	}
	err := h.db.QueryRowContext(ctx, `
		SELECT status, repository_id, target_branch, project_id
		FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&task.Status, &task.RepoID, &task.Branch, &task.ProjectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("task not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	if task.Status != string(models.TaskStatusPRCreated) {
		respond.Error(w, http.StatusBadRequest,
			fmt.Errorf("task must be in 'pr_created' status, current: %s", task.Status))
		return
	}

	result, err := h.kernel().Evaluate(ctx, capability.Request{
		ActorType: "human",
		User: &models.User{
			ID:             user.UserID,
			OrganizationID: user.OrgID,
			Role:           user.Role,
		},
		Operation: capability.OpDeploy,
		Resource:  fmt.Sprintf("%s/%s", task.ProjectID, task.RepoID),
		Details: map[string]any{
			"organization_id": user.OrgID,
			"task_id":         taskID,
			"environment":     req.Environment,
		},
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("authorize deploy: %w", err))
		return
	}
	if result.Effect == policies.EffectDeny {
		respond.Error(w, http.StatusForbidden, errors.New(result.Reason))
		return
	}
	if result.RequiredApproval {
		respond.Error(w, http.StatusLocked, errors.New(result.Reason))
		return
	}

	var repoOwner, repoName string
	if err := h.db.QueryRowContext(ctx, `
		SELECT owner, name FROM repositories WHERE id = $1 AND deleted_at IS NULL
	`, task.RepoID).Scan(&repoOwner, &repoName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("repository not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	ref := req.Ref
	if ref == "" {
		ref = task.Branch
		if ref == "" {
			ref = "main"
		}
	}

	token := strings.TrimSpace(h.deployToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		respond.Error(w, http.StatusServiceUnavailable, errors.New("deploy token is not configured"))
		return
	}

	deployProvider := h.deployGateway
	if deployProvider == nil {
		deployProvider = gateway.NewGitHubGateway(os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET"))
	}

	deployment, err := deployProvider.CreateDeployment(ctx, &oauth2.Token{AccessToken: token}, repoOwner, repoName, req.Environment, ref)
	if err != nil {
		h.logger.Error("failed to create deployment", "task_id", taskID, "error", err)
		respond.Error(w, http.StatusBadGateway, fmt.Errorf("create deployment: %w", err))
		return
	}

	deploymentID := uuid.New().String()
	now := time.Now().UTC()
	_, err = h.db.ExecContext(ctx, `
		INSERT INTO deployments (id, task_id, environment, ref, provider, external_id, status, url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'github', $5, 'pending', $6, $7, $7)
	`, deploymentID, taskID, req.Environment, ref, fmt.Sprintf("%d", deployment.ID), deployment.URL, now)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("record deployment: %w", err))
		return
	}

	if _, err := h.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'deploying', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, taskID); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("update task status: %w", err))
		return
	}

	if h.eventBus != nil {
		event := events.DeployEvent{
			DeploymentID: deploymentID,
			TaskID:       taskID,
			Environment:  req.Environment,
			Ref:          ref,
			Status:       "pending",
			URL:          deployment.URL,
			Provider:     "github",
		}
		data, _ := json.Marshal(event)
		if pubErr := h.eventBus.Publish(events.DeployTriggered, data); pubErr != nil {
			h.logger.Warn("failed to publish deploy.triggered event", "error", pubErr)
		}
	}

	respond.JSON(w, http.StatusCreated, DeploymentResponse{
		ID:          deploymentID,
		TaskID:      taskID,
		Environment: req.Environment,
		Ref:         ref,
		Provider:    "github",
		Status:      "pending",
		URL:         deployment.URL,
		CreatedAt:   now,
	})
}
