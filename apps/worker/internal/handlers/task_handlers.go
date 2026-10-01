// Package handlers contains event handlers for the worker service.
//
// Task handlers process task lifecycle events:
//   - tasks.created -> trigger spec generation
//   - tasks.approved -> create workspace + start agent run
package handlers

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
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

	runMetadata, err := approvedTaskRunMetadata(event.Data)
	if err != nil {
		return err
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
			return ackMessage(msg) // Task not found, ack to remove from queue
		}
		return fmt.Errorf("load task: %w", err)
	}

	now := time.Now().UTC()
	claimed, err := h.claimApprovedTaskForInitialRun(context.Background(), task.ID, now)
	if err != nil {
		return err
	}
	if !claimed {
		h.logger.Info("initial run already claimed; skipping duplicate approval event", "task_id", task.ID)
		return ackMessage(msg)
	}
	releaseClaim := true
	defer func() {
		if releaseClaim {
			if releaseErr := h.releaseInitialRunClaim(context.Background(), task.ID); releaseErr != nil {
				h.logger.Error("failed to release initial run claim", "task_id", task.ID, "error", releaseErr)
			}
		}
	}()

	// Allocate stable identities before provisioning so retries can carry the same
	// run/workspace identity into the runtime boundary.
	runID, workspaceID := initialRunIdentity(task.ID)
	workspace, err := h.provisionWorkspace(context.Background(), approvedTask{
		ID:            task.ID,
		RepositoryID:  task.RepositoryID,
		TargetBranch:  task.TargetBranch,
		CloneURL:      task.CloneURL,
		DefaultBranch: task.DefaultBranch,
		WorkspaceID:   workspaceID,
		RunID:         runID,
	}, now)
	if err != nil {
		return fmt.Errorf("provision workspace runtime: %w", err)
	}

	baseSHA, err := h.workspaceBaseRevision(context.Background(), workspace)
	if err != nil {
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("resolve workspace base revision: %w", err)
	}

	runMetadata, err = bindRunManifest(runMetadata, models.RunManifestInput{
		RunID:           runID,
		TaskID:          task.ID,
		RepositoryID:    task.RepositoryID,
		BaseSHA:         baseSHA,
		AgentRole:       models.AgentRoleImplementer,
		ExecutionClass:  "standard",
		RuntimeProvider: workspace.RuntimeProvider,
		Resources: models.RunResourceLimits{
			CPUMillis:       defaultWorkspaceCPUMillis,
			MemoryMB:        defaultWorkspaceMemoryMB,
			DiskMB:          defaultWorkspaceDiskMB,
			WallTimeSeconds: defaultWorkspaceWallTimeSeconds,
		},
		Authority: models.RunAuthority{
			Network: false,
			Operations: []string{
				"read_file",
				"write_file",
				"search_files",
				"apply_patch",
				"run_command",
				"list_directory",
				"inspect_repo",
				"get_git_diff",
				"create_commit",
			},
		},
	})
	if err != nil {
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("build run manifest: %w", err)
	}

	tx, err := h.db.BeginTx(context.Background(), nil)
	if err != nil {
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("begin initial run transaction: %w", err)
	}
	txCommitted := false
	defer func() {
		if !txCommitted {
			_ = tx.Rollback()
		}
	}()

	_, err = tx.Exec(`
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
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("create workspace: %w", err)
	}

	result, err := tx.Exec(`
		UPDATE tasks
		SET workspace_id = $1, started_at = COALESCE(started_at, $2), updated_at = $2
		WHERE id = $3 AND status = 'running' AND workspace_id IS NULL AND deleted_at IS NULL
	`, workspaceID, now, task.ID)
	if err != nil {
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("attach workspace to claimed task: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("check workspace attachment: %w", err)
	}
	if rows != 1 {
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("claimed task %s changed before workspace attachment", task.ID)
	}

	// Create agent run and preserve the approval-time admission evidence.
	_, err = tx.Exec(`
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider,
			status, total_cost, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, 'implementer', 'gpt-4o', 'openai', 'queued', 0.0, $4, $5, $5)
	`, runID, task.ID, workspaceID, runMetadata, now)
	if err != nil {
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("create agent run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		h.cleanupProvisionedWorkspace(context.Background(), workspace)
		return fmt.Errorf("commit initial run transaction: %w", err)
	}
	txCommitted = true
	releaseClaim = false

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
	RunID         string
}

const (
	defaultWorkspaceCPUMillis       = 2000
	defaultWorkspaceMemoryMB        = 4096
	defaultWorkspaceDiskMB          = 10240
	defaultWorkspaceWallTimeSeconds = 1800
)

type provisionedWorkspace struct {
	Name             string
	BranchName       string
	BaseBranch       string
	WorktreePath     *string
	RuntimeProvider  string
	RuntimeSessionID *string
	Status           string
}

func (h *TaskHandler) claimApprovedTaskForInitialRun(ctx context.Context, taskID string, now time.Time) (bool, error) {
	result, err := h.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'running', updated_at = $1
		WHERE id = $2 AND status = 'approved' AND workspace_id IS NULL AND deleted_at IS NULL
	`, now, taskID)
	if err != nil {
		return false, fmt.Errorf("claim approved task for initial run: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check initial run claim: %w", err)
	}
	return rows == 1, nil
}

func (h *TaskHandler) releaseInitialRunClaim(ctx context.Context, taskID string) error {
	_, err := h.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'approved', updated_at = $1
		WHERE id = $2 AND status = 'running' AND workspace_id IS NULL AND deleted_at IS NULL
	`, time.Now().UTC(), taskID)
	if err != nil {
		return fmt.Errorf("release initial run claim: %w", err)
	}
	return nil
}

func (h *TaskHandler) cleanupProvisionedWorkspace(ctx context.Context, workspace provisionedWorkspace) {
	if h.runtimeProvider == nil || workspace.RuntimeSessionID == nil || *workspace.RuntimeSessionID == "" {
		return
	}
	if err := h.runtimeProvider.DestroyWorkspace(ctx, *workspace.RuntimeSessionID); err != nil {
		h.logger.Warn("failed to cleanup provisioned workspace after initial-run failure",
			"runtime_session_id", *workspace.RuntimeSessionID,
			"error", err,
		)
	}
}

func (h *TaskHandler) provisionWorkspace(ctx context.Context, task approvedTask, now time.Time) (provisionedWorkspace, error) {
	baseBranch := task.TargetBranch
	if baseBranch == "" {
		baseBranch = task.DefaultBranch
	}
	if baseBranch == "" {
		baseBranch = "main"
	}
	branchName := fmt.Sprintf("agent/%s/initial", shortID(task.ID))
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
		Limits: runtimes.ResourceLimits{
			CPUMillis:       defaultWorkspaceCPUMillis,
			MemoryMB:        defaultWorkspaceMemoryMB,
			DiskMB:          defaultWorkspaceDiskMB,
			WallTimeSeconds: defaultWorkspaceWallTimeSeconds,
		},
		Capabilities: runtimes.RuntimeCapabilities{
			Network: false,
		},
		Metadata: map[string]string{
			"dev_plane_task_id": task.ID,
			"dev_plane_run_id":  task.RunID,
		},
		IdempotencyKey: "workspace:" + task.RunID,
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

func initialRunIdentity(taskID string) (runID, workspaceID string) {
	taskID = strings.TrimSpace(taskID)
	runID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("dev-plane:initial-run:"+taskID)).String()
	workspaceID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("dev-plane:initial-workspace:"+taskID)).String()
	return runID, workspaceID
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func approvedTaskRunMetadata(data json.RawMessage) (string, error) {
	if len(data) == 0 || string(data) == "null" {
		return "{}", nil
	}
	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", fmt.Errorf("decode approved task metadata: %w", err)
	}
	normalized, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode approved task metadata: %w", err)
	}
	return string(normalized), nil
}

func (h *TaskHandler) publishExistingQueuedRun(ctx context.Context, taskID string) (bool, error) {
	if h.db == nil || h.eventBus == nil {
		return false, nil
	}
	rows, err := h.db.QueryContext(ctx, `
		SELECT id, COALESCE(metadata, '{}')
		FROM agent_runs
		WHERE task_id = $1 AND status = 'queued'
		ORDER BY created_at DESC
	`, taskID)
	if err != nil {
		return false, fmt.Errorf("load existing queued runs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var runID, metadata string
		if err := rows.Scan(&runID, &metadata); err != nil {
			return false, fmt.Errorf("scan existing queued run: %w", err)
		}
		initial, err := isInitialQueuedRunMetadata(metadata)
		if err != nil {
			return false, fmt.Errorf("classify queued run %s: %w", runID, err)
		}
		if !initial {
			continue
		}
		if err := h.publishRunTriggered(ctx, runID, taskID, "task_approved_retry"); err != nil {
			return false, err
		}
		h.logger.Info("republished existing initial queued run for approved task", "task_id", taskID, "run_id", runID)
		return true, nil
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate existing queued runs: %w", err)
	}
	return false, nil
}

func isInitialQueuedRunMetadata(raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return true, nil
	}
	var metadata struct {
		Trigger          string          `json:"trigger"`
		HandoffFromRunID string          `json:"handoff_from_run_id"`
		Retry            json.RawMessage `json:"retry"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return false, err
	}
	if len(metadata.Retry) > 0 && string(metadata.Retry) != "null" {
		return false, nil
	}
	if metadata.Trigger == "mailbox_handoff" || metadata.HandoffFromRunID != "" {
		return false, nil
	}
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

func (h *TaskHandler) workspaceBaseRevision(ctx context.Context, workspace provisionedWorkspace) (string, error) {
	if h.runtimeProvider == nil {
		return "", errors.New("runtime provider is required to resolve base revision")
	}
	if workspace.RuntimeSessionID == nil || strings.TrimSpace(*workspace.RuntimeSessionID) == "" {
		return "", errors.New("runtime session is required to resolve base revision")
	}
	result, err := h.runtimeProvider.ExecuteCommand(ctx, *workspace.RuntimeSessionID, runtimes.Command{
		Args:    []string{"git", "rev-parse", "HEAD"},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", errors.New("base revision command returned no result")
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("base revision command failed: %s", strings.TrimSpace(result.Stdout+result.Stderr))
	}
	sha := strings.TrimSpace(result.Stdout)
	if !validGitCommitSHA(sha) {
		return "", fmt.Errorf("base revision is not a canonical git commit SHA: %q", sha)
	}
	return sha, nil
}

func validGitCommitSHA(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func bindRunManifest(rawMetadata string, input models.RunManifestInput) (string, error) {
	var metadata map[string]any
	rawMetadata = strings.TrimSpace(rawMetadata)
	if rawMetadata == "" {
		rawMetadata = "{}"
	}
	if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
		return "", fmt.Errorf("decode run metadata: %w", err)
	}
	manifest, err := models.NewRunManifest(input)
	if err != nil {
		return "", err
	}
	if err := manifest.VerifyDigest(); err != nil {
		return "", err
	}
	metadata["run_manifest"] = manifest
	metadata["run_manifest_digest"] = manifest.Digest
	normalized, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("encode run metadata: %w", err)
	}
	return string(normalized), nil
}
