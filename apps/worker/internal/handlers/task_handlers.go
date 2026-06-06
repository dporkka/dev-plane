// Package handlers contains event handlers for the worker service.
//
// Task handlers process task lifecycle events:
//   - tasks.created -> trigger spec generation
//   - tasks.approved -> create workspace + start agent run
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/runtimes"
)

// TaskHandler handles task-related events.
type TaskHandler struct {
	db              *sql.DB
	logger          *slog.Logger
	eventBus        WorkerEventPublisher
	runtimeProvider runtimes.Provider
	runtimeName     string
}

// NewTaskHandler creates a new task handler.
func NewTaskHandler(db *sql.DB, logger *slog.Logger) *TaskHandler {
	return &TaskHandler{db: db, logger: logger}
}

// WithEventPublisher enables publishing follow-on run events.
func (h *TaskHandler) WithEventPublisher(eventBus WorkerEventPublisher) *TaskHandler {
	h.eventBus = eventBus
	return h
}

// WithRuntimeProvider enables real workspace provisioning for approved tasks.
func (h *TaskHandler) WithRuntimeProvider(provider runtimes.Provider, name string) *TaskHandler {
	h.runtimeProvider = provider
	h.runtimeName = name
	return h
}

// HandleTaskCreated processes tasks.created events.
// Triggers spec generation for the task.
func (h *TaskHandler) HandleTaskCreated(msg *nats.Msg) error {
	var event events.TaskEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal task event: %w", err)
	}

	h.logger.Info("handling task created", "task_id", event.TaskID)

	// Update task status to spec_review to trigger spec generation
	now := time.Now().UTC()
	_, err := h.db.Exec(`
		UPDATE tasks SET status = 'spec_review', updated_at = $1
		WHERE id = $2 AND status = 'backlog' AND deleted_at IS NULL
	`, now, event.TaskID)
	if err != nil {
		return fmt.Errorf("update task status for spec generation: %w", err)
	}

	h.logger.Info("task transitioned to spec_review", "task_id", event.TaskID)
	return msg.Ack()
}

// HandleTaskApproved processes tasks.approved events.
// 1. Create workspace for the task
// 2. Start agent run in the workspace
func (h *TaskHandler) HandleTaskApproved(msg *nats.Msg) error {
	var event events.TaskEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal task event: %w", err)
	}

	h.logger.Info("handling task approved", "task_id", event.TaskID)

	if resumed, err := h.publishExistingQueuedRun(context.Background(), event.TaskID); err != nil {
		return err
	} else if resumed {
		return ackMessage(msg)
	}

	// Load task details
	var task struct {
		ID            string
		RepositoryID  string
		TargetBranch  string
		CloneURL      string
		DefaultBranch string
	}
	err := h.db.QueryRow(`
		SELECT t.id, t.repository_id, t.target_branch, r.clone_url, r.default_branch
		FROM tasks t
		JOIN repositories r ON r.id = t.repository_id
		WHERE t.id = $1 AND t.deleted_at IS NULL AND r.deleted_at IS NULL
	`, event.TaskID).Scan(&task.ID, &task.RepositoryID, &task.TargetBranch, &task.CloneURL, &task.DefaultBranch)
	if err != nil {
		if err == sql.ErrNoRows {
			return msg.Ack() // Task not found, ack to remove from queue
		}
		return fmt.Errorf("load task: %w", err)
	}

	// Create workspace
	workspaceID := uuid.New().String()
	now := time.Now().UTC()
	workspace, err := h.provisionWorkspace(context.Background(), approvedTask{
		ID:            task.ID,
		RepositoryID:  task.RepositoryID,
		TargetBranch:  task.TargetBranch,
		CloneURL:      task.CloneURL,
		DefaultBranch: task.DefaultBranch,
		WorkspaceID:   workspaceID,
	}, now)
	if err != nil {
		return fmt.Errorf("provision workspace runtime: %w", err)
	}

	_, err = h.db.Exec(`
		INSERT INTO workspaces (
			id, repository_id, task_id, name, branch, base_branch,
			worktree_path, runtime_provider, runtime_session_id, status,
			created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
	`, workspaceID, task.RepositoryID, task.ID,
		workspace.Name,
		workspace.BranchName, workspace.BaseBranch,
		workspace.WorktreePath, workspace.RuntimeProvider, workspace.RuntimeSessionID, workspace.Status,
		now,
	)
	if err != nil {
		return fmt.Errorf("create workspace: %w", err)
	}

	// Update task with workspace ID and transition to running
	_, err = h.db.Exec(`
		UPDATE tasks SET workspace_id = $1, status = 'running', started_at = $2, updated_at = $2
		WHERE id = $3 AND deleted_at IS NULL
	`, workspaceID, now, task.ID)
	if err != nil {
		return fmt.Errorf("update task with workspace: %w", err)
	}

	// Create agent run
	runID := uuid.New().String()
	_, err = h.db.Exec(`
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider,
			status, total_cost, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, 'implementer', 'gpt-4o', 'openai', 'queued', 0.0, '{}', $4, $4)
	`, runID, task.ID, workspaceID, now)
	if err != nil {
		return fmt.Errorf("create agent run: %w", err)
	}

	h.logger.Info("workspace and agent run created",
		"task_id", task.ID,
		"workspace_id", workspaceID,
		"run_id", runID,
		"branch", workspace.BranchName,
		"runtime_provider", workspace.RuntimeProvider,
		"runtime_session_id", workspace.RuntimeSessionID,
	)

	if err := h.publishRunTriggered(context.Background(), runID, task.ID, "task_approved"); err != nil {
		return err
	}

	return ackMessage(msg)
}

type approvedTask struct {
	ID            string
	RepositoryID  string
	TargetBranch  string
	CloneURL      string
	DefaultBranch string
	WorkspaceID   string
}

type provisionedWorkspace struct {
	Name             string
	BranchName       string
	BaseBranch       string
	WorktreePath     *string
	RuntimeProvider  string
	RuntimeSessionID *string
	Status           string
}

func (h *TaskHandler) provisionWorkspace(ctx context.Context, task approvedTask, now time.Time) (provisionedWorkspace, error) {
	baseBranch := task.TargetBranch
	if baseBranch == "" {
		baseBranch = task.DefaultBranch
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	branchName := fmt.Sprintf("agent/%s/%d", shortID(task.ID), now.Unix())
	workspace := provisionedWorkspace{
		Name:            fmt.Sprintf("workspace-%s", shortID(task.ID)),
		BranchName:      branchName,
		BaseBranch:      baseBranch,
		RuntimeProvider: h.runtimeName,
		Status:          "pending",
	}
	if workspace.RuntimeProvider == "" {
		workspace.RuntimeProvider = "unprovisioned"
	}
	if h.runtimeProvider == nil {
		return workspace, nil
	}

	provisionCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	session, err := h.runtimeProvider.CreateWorkspace(provisionCtx, runtimes.CreateRequest{
		RepositoryID: task.RepositoryID,
		CloneURL:     task.CloneURL,
		Branch:       branchName,
		BaseBranch:   baseBranch,
		WorktreeName: workspace.Name,
	})
	if err != nil {
		return provisionedWorkspace{}, err
	}
	workspace.Status = session.Status
	if session.Provider != "" {
		workspace.RuntimeProvider = session.Provider
	}
	if session.ID != "" {
		workspace.RuntimeSessionID = &session.ID
	}
	if session.WorktreePath != "" {
		workspace.WorktreePath = &session.WorktreePath
	}
	return workspace, nil
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func (h *TaskHandler) publishExistingQueuedRun(ctx context.Context, taskID string) (bool, error) {
	if h.db == nil || h.eventBus == nil {
		return false, nil
	}
	var runID string
	err := h.db.QueryRowContext(ctx, `
		SELECT id
		FROM agent_runs
		WHERE task_id = $1 AND status = 'queued'
		ORDER BY created_at DESC
		LIMIT 1
	`, taskID).Scan(&runID)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("load existing queued run: %w", err)
	}
	if err := h.publishRunTriggered(ctx, runID, taskID, "task_approved_retry"); err != nil {
		return false, err
	}
	h.logger.Info("republished existing queued run for approved task", "task_id", taskID, "run_id", runID)
	return true, nil
}

func (h *TaskHandler) publishRunTriggered(ctx context.Context, runID, taskID, action string) error {
	_ = ctx
	if h.eventBus == nil {
		return nil
	}
	payload := map[string]any{
		"run_id":  runID,
		"task_id": taskID,
		"status":  "queued",
		"action":  action,
	}
	data, _ := json.Marshal(payload)
	if err := h.eventBus.Publish(events.RunTriggered, data); err != nil {
		return fmt.Errorf("publish run triggered: %w", err)
	}
	return nil
}
