// Package handlers contains event handlers for the worker service.
//
// Task handlers process task lifecycle events:
//   - tasks.created -> trigger spec generation
//   - tasks.approved -> create workspace + start agent run
package handlers

import (
	"context"
	"database/sql"
	"errors"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/scheduler"
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

type schedulerAdmissionDecision struct {
	Allowed bool
	Reason  string
}

type schedulerTaskClaim struct {
	ID   string
	Owns []string
}

func (h *TaskHandler) schedulerAdmission(ctx context.Context, taskID string) (schedulerAdmissionDecision, error) {
	if h.db == nil {
		return schedulerAdmissionDecision{Allowed: true}, nil
	}

	var projectID, repositoryID string
	if err := h.db.QueryRowContext(ctx, `
		SELECT project_id, repository_id
		FROM tasks
		WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(&projectID, &repositoryID); err != nil {
		return schedulerAdmissionDecision{}, fmt.Errorf("load task scheduler scope: %w", err)
	}

	var maxConcurrent int
	err := h.db.QueryRowContext(ctx, `
		SELECT max_concurrent_agents
		FROM budgets
		WHERE project_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, projectID).Scan(&maxConcurrent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return schedulerAdmissionDecision{}, fmt.Errorf("load project concurrency budget: %w", err)
	}
	if maxConcurrent > 0 {
		var active int
		if err := h.db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM tasks
			WHERE project_id = $1
			  AND status = 'running'
			  AND deleted_at IS NULL
		`, projectID).Scan(&active); err != nil {
			return schedulerAdmissionDecision{}, fmt.Errorf("count active project tasks: %w", err)
		}
		if active >= maxConcurrent {
			return schedulerAdmissionDecision{Allowed: false, Reason: "concurrency-budget"}, nil
		}
	}

	candidateOwns, candidateKnown, err := h.loadTaskOwnership(ctx, taskID)
	if err != nil {
		return schedulerAdmissionDecision{}, err
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT id
		FROM tasks
		WHERE repository_id = $1
		  AND id <> $2
		  AND status = 'running'
		  AND deleted_at IS NULL
		ORDER BY id ASC
	`, repositoryID, taskID)
	if err != nil {
		return schedulerAdmissionDecision{}, fmt.Errorf("load active repository tasks: %w", err)
	}
	activeIDs := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return schedulerAdmissionDecision{}, fmt.Errorf("scan active repository task: %w", err)
		}
		activeIDs = append(activeIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return schedulerAdmissionDecision{}, fmt.Errorf("iterate active repository tasks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return schedulerAdmissionDecision{}, fmt.Errorf("close active repository tasks: %w", err)
	}

	running := make([]schedulerTaskClaim, 0, len(activeIDs))
	for _, id := range activeIDs {
		owns, known, err := h.loadTaskOwnership(ctx, id)
		if err != nil {
			return schedulerAdmissionDecision{}, err
		}
		if !known {
			return schedulerAdmissionDecision{Allowed: false, Reason: "ownership-unknown"}, nil
		}
		running = append(running, schedulerTaskClaim{ID: id, Owns: owns})
	}

	if len(running) == 0 {
		return schedulerAdmissionDecision{Allowed: true}, nil
	}
	if !candidateKnown {
		return schedulerAdmissionDecision{Allowed: false, Reason: "ownership-unknown"}, nil
	}

	tasks := make([]scheduler.Task, 0, len(running)+1)
	state := scheduler.State{}
	for _, claim := range running {
		tasks = append(tasks, scheduler.Task{
			ID: claim.ID, Owns: claim.Owns,
			Resources: scheduler.Resources{CPU: 1, MemoryMB: 1},
		})
		state[claim.ID] = scheduler.StatusRunning
	}
	tasks = append(tasks, scheduler.Task{
		ID: taskID, Owns: candidateOwns,
		Resources: scheduler.Resources{CPU: 1, MemoryMB: 1},
	})
	state[taskID] = scheduler.StatusPending

	parallel := len(tasks)
	decision, err := scheduler.Next(scheduler.Manifest{
		MaxParallel: parallel,
		Capacity: scheduler.Capacity{CPU: float64(parallel), MemoryMB: parallel},
		Tasks: tasks,
	}, state)
	if err != nil {
		return schedulerAdmissionDecision{}, fmt.Errorf("evaluate scheduler admission: %w", err)
	}
	for _, ready := range decision.Ready {
		if ready == taskID {
			return schedulerAdmissionDecision{Allowed: true}, nil
		}
	}
	return schedulerAdmissionDecision{Allowed: false, Reason: "ownership-conflict"}, nil
}

func (h *TaskHandler) loadTaskOwnership(ctx context.Context, taskID string) ([]string, bool, error) {
	var changedJSON, createdJSON string
	err := h.db.QueryRowContext(ctx, `
		SELECT files_to_change, files_to_create
		FROM task_specs
		WHERE task_id = $1
		LIMIT 1
	`, taskID).Scan(&changedJSON, &createdJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load task ownership for %s: %w", taskID, err)
	}

	var changed, created []string
	if err := json.Unmarshal([]byte(changedJSON), &changed); err != nil {
		return nil, false, fmt.Errorf("decode files_to_change for %s: %w", taskID, err)
	}
	if err := json.Unmarshal([]byte(createdJSON), &created); err != nil {
		return nil, false, fmt.Errorf("decode files_to_create for %s: %w", taskID, err)
	}
	seen := map[string]struct{}{}
	owns := make([]string, 0, len(changed)+len(created))
	for _, value := range append(changed, created...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		owns = append(owns, value)
	}
	return owns, len(owns) > 0, nil
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

	admission, err := h.schedulerAdmission(context.Background(), event.TaskID)
	if err != nil {
		return fmt.Errorf("scheduler admission: %w", err)
	}
	if !admission.Allowed {
		h.logger.Info("deferring approved task for scheduler admission",
			"task_id", event.TaskID,
			"reason", admission.Reason,
		)
		if msg != nil {
			if err := msg.NakWithDelay(30 * time.Second); err != nil && !errors.Is(err, nats.ErrMsgNoReply) {
				h.logger.Warn("failed to defer task approval message", "task_id", event.TaskID, "error", err)
			}
		}
		return nil
	}

	// Load task details
	var task struct {
		ID            string
		RepositoryID  string
		TargetBranch  string
		CloneURL      string
		DefaultBranch string
	}
	err = h.db.QueryRow(`
		SELECT t.id, t.repository_id, t.target_branch, r.clone_url, r.default_branch
		FROM tasks t
		JOIN repositories r ON r.id = t.repository_id
		WHERE t.id = $1 AND t.deleted_at IS NULL AND r.deleted_at IS NULL
	`, event.TaskID).Scan(&task.ID, &task.RepositoryID, &task.TargetBranch, &task.CloneURL, &task.DefaultBranch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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
		if errors.Is(err, sql.ErrNoRows) {
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
