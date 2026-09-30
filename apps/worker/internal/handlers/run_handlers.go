// Package handlers contains event handlers for the worker service.
//
// Run handlers process agent run lifecycle events:
//   - agents.run.completed -> consume mailbox handoffs or review completed work
//   - agents.run.failed -> transition task to failed and notify integrations
//   - review.completed -> request human approval for PR creation
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
	runfailure "github.com/ai-dev-control-plane/failure"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/runtimes"
)

// RunHandler handles agent run lifecycle events.
type RunHandler struct {
	db        *sql.DB
	logger    *slog.Logger
	eventBus  WorkerEventPublisher
	executor  RunExecutor
	reviewer        ReviewService
	admission       RunAdmission
	runtimeProvider runtimes.Provider
	runtimeName     string
}

// RunExecutor executes queued agent runs.
type RunExecutor interface {
	ExecuteRun(ctx context.Context, runID string) error
}

var ErrRunAdmissionDeferred = errors.New("run admission deferred")

const maxAutomaticRunRetries = 2

type RunAdmissionDecision struct {
	Allowed    bool
	Reason     string
	RetryAfter time.Duration
}

type RunAdmission interface {
	AdmitRun(ctx context.Context, runID, taskID string) (RunAdmissionDecision, error)
	ReleaseRun(ctx context.Context, runID string) error
}

// ReviewService reviews completed agent runs and persists review reports.
type ReviewService interface {
	Review(ctx context.Context, runID string) (*reviewer.ReviewReport, error)
}

// NewRunHandler creates a new run handler.
func NewRunHandler(db *sql.DB, logger *slog.Logger, eventBus WorkerEventPublisher) *RunHandler {
	return &RunHandler{db: db, logger: logger, eventBus: eventBus}
}

// WithRunExecutor enables runs.triggered execution dispatch.
func (h *RunHandler) WithRunExecutor(executor RunExecutor) *RunHandler {
	h.executor = executor
	return h
}

func (h *RunHandler) WithRunAdmission(admission RunAdmission) *RunHandler {
	h.admission = admission
	return h
}

// WithReviewer enables completed-run review generation before approval flow.
func (h *RunHandler) WithReviewer(reviewer ReviewService) *RunHandler {
	h.reviewer = reviewer
	return h
}

// WithRuntimeProvider enables isolated fresh-environment recovery for failures
// that explicitly request retry_fresh_environment.
func (h *RunHandler) WithRuntimeProvider(provider runtimes.Provider, name string) *RunHandler {
	h.runtimeProvider = provider
	h.runtimeName = strings.TrimSpace(name)
	return h
}

// WithEventPublisher replaces the event publisher, primarily for tests.
func (h *RunHandler) WithEventPublisher(eventBus WorkerEventPublisher) *RunHandler {
	h.eventBus = eventBus
	return h
}

// HandleRunCompleted processes agents.run.completed events.
// 1. Queue the next role when the run produced a mailbox handoff
// 2. Generate and persist a review report when there is no handoff
func (h *RunHandler) HandleRunCompleted(msg *nats.Msg) error {
	var event events.AgentRunEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal agent run event: %w", err)
	}

	h.logger.Info("handling run completed", "run_id", event.RunID, "task_id", event.TaskID)

	if scheduled, nextRunID, nextRole, err := h.scheduleFollowOnRun(context.Background(), event); err != nil {
		return err
	} else if scheduled {
		h.logger.Info("scheduled follow-on agent run from mailbox handoff",
			"run_id", event.RunID,
			"next_run_id", nextRunID,
			"next_role", nextRole,
		)
		return ackMessage(msg)
	}

	// Update task status to reviewing
	now := time.Now().UTC()
	_, err := h.db.Exec(`
		UPDATE tasks SET status = 'reviewing', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, event.TaskID)
	if err != nil {
		h.logger.Warn("failed to update task status to reviewing", "error", err)
	}

	if h.reviewer == nil {
		return fmt.Errorf("reviewer is not configured")
	}

	report, err := h.reviewer.Review(context.Background(), event.RunID)
	if err != nil {
		return fmt.Errorf("review completed run %s: %w", event.RunID, err)
	}
	if h.eventBus != nil {
		reviewCompletedEvent := map[string]interface{}{
			"run_id":     event.RunID,
			"task_id":    event.TaskID,
			"status":     "completed",
			"risk_level": report.RiskLevel,
			"approvable": report.Approvable,
			"timestamp":  now.Format(time.RFC3339),
		}
		data, _ := json.Marshal(reviewCompletedEvent)
		if pubErr := h.eventBus.Publish("review.completed", data); pubErr != nil {
			return fmt.Errorf("publish review.completed event: %w", pubErr)
		}
	}

	h.logger.Info("run completion processed, review completed",
		"run_id", event.RunID,
		"risk_level", report.RiskLevel,
		"approvable", report.Approvable,
	)
	return ackMessage(msg)
}

// HandleRunFailed processes agents.run.failed events. Retryable failures with
// an explicit same-environment retry disposition are retried within a bounded
// budget; all other failures transition the task to failed.
func (h *RunHandler) HandleRunFailed(msg *nats.Msg) error {
	var event events.AgentRunEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal agent run event: %w", err)
	}

	h.logger.Info("handling run failed", "run_id", event.RunID, "task_id", event.TaskID)

	var failureData struct {
		Error   string                    `json:"error"`
		Failure runfailure.Classification `json:"failure"`
	}
	if len(event.Data) > 0 {
		if err := json.Unmarshal(event.Data, &failureData); err != nil {
			return fmt.Errorf("unmarshal agent run failure data: %w", err)
		}
	}

	if scheduled, retryRunID, err := h.scheduleAutomaticRetry(
		context.Background(),
		event,
		failureData.Failure,
	); err != nil {
		return err
	} else if scheduled {
		h.logger.Info(
			"run failure scheduled for automatic retry",
			"run_id", event.RunID,
			"retry_run_id", retryRunID,
			"task_id", event.TaskID,
			"failure_category", failureData.Failure.Category,
		)
		return ackMessage(msg)
	}

	now := time.Now().UTC()
	_, err := h.db.Exec(`
		UPDATE tasks SET status = 'failed', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, event.TaskID)
	if err != nil {
		return fmt.Errorf("update task status to failed: %w", err)
	}

	if h.eventBus != nil {
		failedEvent := events.TaskEvent{
			TaskID: event.TaskID,
			Status: string(models.TaskStatusFailed),
			Data:   event.Data,
		}
		data, _ := json.Marshal(failedEvent)
		if pubErr := h.eventBus.Publish(events.TaskFailed, data); pubErr != nil {
			return fmt.Errorf("publish tasks.failed event: %w", pubErr)
		}
	}

	h.logger.Info("run failure processed, task transitioned to failed",
		"run_id", event.RunID,
		"task_id", event.TaskID,
		"failure_category", failureData.Failure.Category,
	)
	return ackMessage(msg)
}

func (h *RunHandler) scheduleAutomaticRetry(
	ctx context.Context,
	event events.AgentRunEvent,
	classification runfailure.Classification,
) (bool, string, error) {
	if h.db == nil || h.eventBus == nil || strings.TrimSpace(event.RunID) == "" {
		return false, "", nil
	}

	run, err := h.loadCompletedRunContext(ctx, event)
	if err != nil {
		return false, "", err
	}

	metadataValues := map[string]any{}
	if strings.TrimSpace(run.Metadata) != "" {
		if err := json.Unmarshal([]byte(run.Metadata), &metadataValues); err != nil {
			return false, "", fmt.Errorf("decode failed run metadata: %w", err)
		}
	}
	attempts := automaticRetryAttempt(metadataValues)
	decision := runfailure.DecideAutoRetry(classification, attempts, maxAutomaticRunRetries)
	if !decision.Retry {
		return false, "", nil
	}
	if decision.Mode == runfailure.RetryModeFreshEnvironment &&
		(h.runtimeProvider == nil || run.WorkspaceID == nil || strings.TrimSpace(*run.WorkspaceID) == "") {
		return false, "", nil
	}

	retryRunID := uuid.NewSHA1(
		uuid.NameSpaceOID,
		[]byte(fmt.Sprintf("dev-plane:auto-retry:%s:%d", run.RunID, decision.NextAttempt)),
	).String()

	exists, err := h.automaticRetryExists(ctx, retryRunID)
	if err != nil {
		return false, "", err
	}
	if exists {
		if err := h.dispatchAutomaticRetry(ctx, retryRunID, run, decision, classification); err != nil {
			return false, "", err
		}
		return true, retryRunID, nil
	}

	rootRunID := run.RunID
	if retry, ok := metadataValues["retry"].(map[string]any); ok {
		if value, ok := retry["root_run_id"].(string); ok && strings.TrimSpace(value) != "" {
			rootRunID = value
		}
	}
	delete(metadataValues, "failure")
	retryMetadata := map[string]any{
		"automatic":        true,
		"mode":             decision.Mode,
		"auto_attempt":     decision.NextAttempt,
		"original_run_id":  run.RunID,
		"root_run_id":      rootRunID,
		"previous_failure": classification,
	}
	metadataValues["retry"] = retryMetadata

	workspaceArg := any(nil)
	if run.WorkspaceID != nil {
		workspaceArg = *run.WorkspaceID
	}

	var fresh *freshRetryWorkspace
	if decision.Mode == runfailure.RetryModeFreshEnvironment {
		fresh, err = h.prepareFreshRetryWorkspace(ctx, run, retryRunID, decision.NextAttempt)
		if err != nil {
			return false, "", err
		}
		workspaceArg = fresh.ID
		retryMetadata["from_workspace_id"] = fresh.FromWorkspaceID
		retryMetadata["to_workspace_id"] = fresh.ID
		retryMetadata["base_revision"] = fresh.BaseRevision
		retryMetadata["subject_revision"] = fresh.SubjectRevision
	}

	metadata, err := json.Marshal(metadataValues)
	if err != nil {
		if fresh != nil {
			h.cleanupFreshRetryWorkspace(ctx, fresh)
		}
		return false, "", fmt.Errorf("marshal automatic retry metadata: %w", err)
	}

	now := time.Now().UTC()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		if fresh != nil {
			h.cleanupFreshRetryWorkspace(ctx, fresh)
		}
		return false, "", fmt.Errorf("begin automatic retry transaction: %w", err)
	}
	defer tx.Rollback()

	if fresh != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO workspaces (
				id, repository_id, task_id, name, branch, base_branch,
				worktree_path, runtime_provider, runtime_session_id, status,
				settings, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
		`,
			fresh.ID,
			fresh.RepositoryID,
			run.TaskID,
			fresh.Name,
			fresh.Branch,
			fresh.BaseBranch,
			fresh.WorktreePath,
			fresh.RuntimeProvider,
			fresh.RuntimeSessionID,
			fresh.Status,
			fresh.Settings,
			now,
		); err != nil {
			h.cleanupFreshRetryWorkspace(ctx, fresh)
			return false, "", fmt.Errorf("persist fresh retry workspace: %w", err)
		}
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider,
			status, total_cost, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'queued', 0.0, $7, $8, $8)
		ON CONFLICT(id) DO NOTHING
	`, retryRunID, run.TaskID, workspaceArg, run.AgentRole, run.Model, run.Provider, string(metadata), now)
	if err != nil {
		if fresh != nil {
			h.cleanupFreshRetryWorkspace(ctx, fresh)
		}
		return false, "", fmt.Errorf("create automatic retry run: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		if fresh != nil {
			h.cleanupFreshRetryWorkspace(ctx, fresh)
		}
		return false, "", fmt.Errorf("check automatic retry creation: %w", err)
	}
	if rows == 0 {
		if fresh != nil {
			h.cleanupFreshRetryWorkspace(ctx, fresh)
		}
		return false, "", fmt.Errorf("automatic retry %s was concurrently created", retryRunID)
	}

	if fresh != nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks
			SET status = 'running', workspace_id = $1, updated_at = $2
			WHERE id = $3 AND deleted_at IS NULL
		`, fresh.ID, now, run.TaskID); err != nil {
			h.cleanupFreshRetryWorkspace(ctx, fresh)
			return false, "", fmt.Errorf("attach fresh retry workspace to task: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE tasks SET status = 'running', updated_at = $1
			WHERE id = $2 AND deleted_at IS NULL
		`, now, run.TaskID); err != nil {
			return false, "", fmt.Errorf("update task for automatic retry: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		if fresh != nil {
			h.cleanupFreshRetryWorkspace(ctx, fresh)
		}
		return false, "", fmt.Errorf("commit automatic retry: %w", err)
	}

	if err := h.dispatchAutomaticRetry(ctx, retryRunID, run, decision, classification); err != nil {
		return false, "", err
	}
	return true, retryRunID, nil
}

type freshRetryWorkspace struct {
	ID               string
	RepositoryID     string
	FromWorkspaceID  string
	Name             string
	Branch           string
	BaseBranch       string
	WorktreePath     any
	RuntimeProvider  string
	RuntimeSessionID string
	Status           string
	Settings         string
	BaseRevision     string
	SubjectRevision  string
}

func (h *RunHandler) automaticRetryExists(ctx context.Context, runID string) (bool, error) {
	var found string
	err := h.db.QueryRowContext(ctx, `SELECT id FROM agent_runs WHERE id = $1`, runID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check existing automatic retry: %w", err)
	}
	return true, nil
}

func (h *RunHandler) dispatchAutomaticRetry(
	ctx context.Context,
	retryRunID string,
	run *completedRunContext,
	decision runfailure.AutoRetryDecision,
	classification runfailure.Classification,
) error {
	dispatched, err := h.automaticRetryDispatchPublished(ctx, retryRunID)
	if err != nil {
		return err
	}
	if dispatched {
		return nil
	}

	payload := events.RunEvent{
		RunID:  retryRunID,
		TaskID: run.TaskID,
		Status: "queued",
	}
	payload.Data, _ = json.Marshal(map[string]any{
		"action":          "automatic_retry",
		"mode":            decision.Mode,
		"previous_run_id": run.RunID,
		"auto_attempt":    decision.NextAttempt,
		"failure":         classification,
	})
	data, _ := json.Marshal(payload)
	if err := h.eventBus.Publish(events.RunTriggered, data); err != nil {
		return fmt.Errorf("publish automatic retry run: %w", err)
	}
	return h.markAutomaticRetryDispatchPublished(ctx, retryRunID)
}

func (h *RunHandler) prepareFreshRetryWorkspace(
	ctx context.Context,
	run *completedRunContext,
	retryRunID string,
	attempt int,
) (*freshRetryWorkspace, error) {
	if h.runtimeProvider == nil || run.WorkspaceID == nil {
		return nil, errors.New("fresh retry runtime is not configured")
	}

	var source struct {
		ID               string
		RepositoryID     string
		Name             string
		Branch           string
		BaseBranch       string
		RuntimeProvider  string
		RuntimeSessionID string
		CloneURL         string
		DefaultBranch    string
	}
	err := h.db.QueryRowContext(ctx, `
		SELECT w.id, w.repository_id, w.name, w.branch, w.base_branch,
		       w.runtime_provider, COALESCE(w.runtime_session_id, ''),
		       r.clone_url, r.default_branch
		FROM workspaces w
		JOIN repositories r ON r.id = w.repository_id
		WHERE w.id = $1 AND r.deleted_at IS NULL
	`, *run.WorkspaceID).Scan(
		&source.ID,
		&source.RepositoryID,
		&source.Name,
		&source.Branch,
		&source.BaseBranch,
		&source.RuntimeProvider,
		&source.RuntimeSessionID,
		&source.CloneURL,
		&source.DefaultBranch,
	)
	if err != nil {
		return nil, fmt.Errorf("load source workspace for fresh retry: %w", err)
	}
	if strings.TrimSpace(source.RuntimeSessionID) == "" {
		return nil, errors.New("source workspace runtime session id is missing")
	}
	if strings.TrimSpace(source.BaseBranch) == "" {
		source.BaseBranch = source.DefaultBranch
	}
	if strings.TrimSpace(source.BaseBranch) == "" {
		return nil, errors.New("source workspace base branch is missing")
	}

	baseRevision, patch, subjectRevision, err := h.captureFreshRetrySource(
		ctx,
		source.RuntimeSessionID,
		source.BaseBranch,
	)
	if err != nil {
		return nil, err
	}

	workspaceID := uuid.NewSHA1(
		uuid.NameSpaceOID,
		[]byte("dev-plane:fresh-workspace:"+retryRunID),
	).String()
	name := fmt.Sprintf("%s-retry-%d", source.Name, attempt)
	session, err := h.runtimeProvider.CreateWorkspace(ctx, runtimes.CreateRequest{
		RepositoryID: source.RepositoryID,
		CloneURL:     source.CloneURL,
		Branch:       source.Branch,
		BaseBranch:   source.BaseBranch,
		Revision:     baseRevision,
		WorktreeName: name,
	})
	if err != nil {
		return nil, fmt.Errorf("provision fresh retry workspace: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup && session != nil && strings.TrimSpace(session.ID) != "" {
			_ = h.runtimeProvider.DestroyWorkspace(context.Background(), session.ID)
		}
	}()

	if strings.TrimSpace(patch) != "" {
		if err := h.runtimeProvider.ApplyPatch(ctx, session.ID, patch); err != nil {
			return nil, fmt.Errorf("restore fresh retry source patch: %w", err)
		}
	}
	reproducedRevision, err := h.runtimeWorkspaceTreeRevision(ctx, session.ID, baseRevision)
	if err != nil {
		return nil, fmt.Errorf("verify fresh retry workspace: %w", err)
	}
	if reproducedRevision != subjectRevision {
		return nil, fmt.Errorf(
			"fresh retry tree mismatch: source %s reproduced %s",
			subjectRevision,
			reproducedRevision,
		)
	}

	runtimeProvider := strings.TrimSpace(session.Provider)
	if runtimeProvider == "" {
		runtimeProvider = h.runtimeName
	}
	if runtimeProvider == "" {
		runtimeProvider = source.RuntimeProvider
	}
	status := strings.TrimSpace(session.Status)
	if status == "" {
		status = models.WorkspaceStatusReady
	}
	var worktreePath any
	if strings.TrimSpace(session.WorktreePath) != "" {
		worktreePath = session.WorktreePath
	}
	settingsBytes, _ := json.Marshal(map[string]any{
		"retry": map[string]any{
			"from_workspace_id": source.ID,
			"base_revision":     baseRevision,
			"subject_revision":  subjectRevision,
			"auto_attempt":      attempt,
		},
	})

	cleanup = false
	return &freshRetryWorkspace{
		ID:               workspaceID,
		RepositoryID:     source.RepositoryID,
		FromWorkspaceID:  source.ID,
		Name:             name,
		Branch:           source.Branch,
		BaseBranch:       source.BaseBranch,
		WorktreePath:     worktreePath,
		RuntimeProvider:  runtimeProvider,
		RuntimeSessionID: session.ID,
		Status:           status,
		Settings:         string(settingsBytes),
		BaseRevision:     baseRevision,
		SubjectRevision:  subjectRevision,
	}, nil
}

func (h *RunHandler) captureFreshRetrySource(
	ctx context.Context,
	sessionID string,
	baseBranch string,
) (string, string, string, error) {
	ref := "refs/remotes/origin/" + strings.TrimSpace(baseBranch)
	result, err := h.runtimeProvider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Args:    []string{"git", "rev-parse", ref},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("resolve fresh retry base revision: %w", err)
	}
	if result == nil || result.ExitCode != 0 {
		return "", "", "", fmt.Errorf("resolve fresh retry base revision failed")
	}
	baseRevision := strings.TrimSpace(result.Stdout)
	if baseRevision == "" {
		return "", "", "", errors.New("fresh retry base revision is empty")
	}

	const patchCommand = `tmp=$(mktemp); rm -f "$tmp"; trap 'rm -f "$tmp"' EXIT; GIT_INDEX_FILE="$tmp" git read-tree "$DEV_PLANE_BASE_REVISION" >/dev/null && GIT_INDEX_FILE="$tmp" git add -A >/dev/null && GIT_INDEX_FILE="$tmp" git diff --cached --binary --full-index "$DEV_PLANE_BASE_REVISION" --`
	patchResult, err := h.runtimeProvider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Command:     patchCommand,
		Env:         map[string]string{"DEV_PLANE_BASE_REVISION": baseRevision},
		Timeout:     60 * time.Second,
		UnsafeShell: true,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("capture fresh retry source patch: %w", err)
	}
	if patchResult == nil || patchResult.ExitCode != 0 {
		return "", "", "", errors.New("capture fresh retry source patch failed")
	}

	subjectRevision, err := h.runtimeWorkspaceTreeRevision(ctx, sessionID, baseRevision)
	if err != nil {
		return "", "", "", fmt.Errorf("capture fresh retry source tree: %w", err)
	}
	return baseRevision, patchResult.Stdout, subjectRevision, nil
}

func (h *RunHandler) runtimeWorkspaceTreeRevision(
	ctx context.Context,
	sessionID string,
	baseRevision string,
) (string, error) {
	const treeCommand = `tmp=$(mktemp); rm -f "$tmp"; trap 'rm -f "$tmp"' EXIT; GIT_INDEX_FILE="$tmp" git read-tree "$DEV_PLANE_BASE_REVISION" >/dev/null && GIT_INDEX_FILE="$tmp" git add -A >/dev/null && GIT_INDEX_FILE="$tmp" git write-tree`
	result, err := h.runtimeProvider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Command:     treeCommand,
		Env:         map[string]string{"DEV_PLANE_BASE_REVISION": baseRevision},
		Timeout:     30 * time.Second,
		UnsafeShell: true,
	})
	if err != nil {
		return "", err
	}
	if result == nil || result.ExitCode != 0 {
		return "", errors.New("workspace tree revision command failed")
	}
	tree := strings.TrimSpace(result.Stdout)
	if tree == "" {
		return "", errors.New("workspace tree revision is empty")
	}
	return "git-tree:" + tree, nil
}

func (h *RunHandler) cleanupFreshRetryWorkspace(ctx context.Context, workspace *freshRetryWorkspace) {
	if h.runtimeProvider == nil || workspace == nil || strings.TrimSpace(workspace.RuntimeSessionID) == "" {
		return
	}
	if err := h.runtimeProvider.DestroyWorkspace(ctx, workspace.RuntimeSessionID); err != nil {
		h.logger.Warn(
			"failed to cleanup fresh retry workspace",
			"workspace_id", workspace.ID,
			"runtime_session_id", workspace.RuntimeSessionID,
			"error", err,
		)
	}
}

func (h *RunHandler) automaticRetryDispatchPublished(ctx context.Context, runID string) (bool, error) {
	var raw sql.NullString
	if err := h.db.QueryRowContext(ctx, `
		SELECT metadata FROM agent_runs WHERE id = $1
	`, runID).Scan(&raw); err != nil {
		return false, fmt.Errorf("load automatic retry dispatch metadata: %w", err)
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return false, nil
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(raw.String), &metadata); err != nil {
		return false, fmt.Errorf("decode automatic retry dispatch metadata: %w", err)
	}
	retry, ok := metadata["retry"].(map[string]any)
	if !ok {
		return false, nil
	}
	dispatched, _ := retry["dispatch_published"].(bool)
	return dispatched, nil
}

func (h *RunHandler) markAutomaticRetryDispatchPublished(ctx context.Context, runID string) error {
	var raw sql.NullString
	if err := h.db.QueryRowContext(ctx, `
		SELECT metadata FROM agent_runs WHERE id = $1
	`, runID).Scan(&raw); err != nil {
		return fmt.Errorf("load automatic retry metadata for dispatch marker: %w", err)
	}
	metadata := map[string]any{}
	if raw.Valid && strings.TrimSpace(raw.String) != "" {
		if err := json.Unmarshal([]byte(raw.String), &metadata); err != nil {
			return fmt.Errorf("decode automatic retry metadata for dispatch marker: %w", err)
		}
	}
	retry, ok := metadata["retry"].(map[string]any)
	if !ok {
		retry = map[string]any{}
		metadata["retry"] = retry
	}
	retry["dispatch_published"] = true
	retry["dispatch_published_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode automatic retry dispatch marker: %w", err)
	}
	if _, err := h.db.ExecContext(ctx, `
		UPDATE agent_runs SET metadata = $1, updated_at = $2 WHERE id = $3
	`, string(encoded), time.Now().UTC(), runID); err != nil {
		return fmt.Errorf("persist automatic retry dispatch marker: %w", err)
	}
	return nil
}

func automaticRetryAttempt(metadata map[string]any) int {
	retry, ok := metadata["retry"].(map[string]any)
	if !ok {
		return 0
	}
	switch value := retry["auto_attempt"].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

type completedRunContext struct {
	RunID       string
	TaskID      string
	WorkspaceID *string
	AgentRole   string
	Model       string
	Provider    string
	Metadata    string
}

type pendingHandoff struct {
	ID      string
	ToAgent string
}

func (h *RunHandler) scheduleFollowOnRun(ctx context.Context, event events.AgentRunEvent) (bool, string, string, error) {
	if h.db == nil || strings.TrimSpace(event.RunID) == "" {
		return false, "", "", nil
	}
	run, err := h.loadCompletedRunContext(ctx, event)
	if err != nil {
		return false, "", "", err
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return false, "", "", fmt.Errorf("begin follow-on run transaction: %w", err)
	}
	defer tx.Rollback()

	handoff, err := h.nextUnconsumedHandoff(ctx, tx, run)
	if err != nil {
		return false, "", "", err
	}
	if handoff == nil {
		return false, "", "", nil
	}
	if !validAgentRole(handoff.ToAgent) {
		return false, "", "", fmt.Errorf("handoff %s targets unknown agent role %q", handoff.ID, handoff.ToAgent)
	}

	nextRunID := uuid.New().String()
	now := time.Now().UTC()
	metadataValues := map[string]any{}
	if strings.TrimSpace(run.Metadata) != "" {
		if err := json.Unmarshal([]byte(run.Metadata), &metadataValues); err != nil {
			return false, "", "", fmt.Errorf("decode parent run metadata: %w", err)
		}
	}
	metadataValues["trigger"] = "mailbox_handoff"
	metadataValues["handoff_message_id"] = handoff.ID
	metadataValues["handoff_from_run_id"] = run.RunID
	metadataValues["handoff_from_agent"] = run.AgentRole
	metadata, err := json.Marshal(metadataValues)
	if err != nil {
		return false, "", "", fmt.Errorf("marshal follow-on run metadata: %w", err)
	}

	workspaceArg := any(nil)
	if run.WorkspaceID != nil {
		workspaceArg = *run.WorkspaceID
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider,
			status, total_cost, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, 'queued', 0.0, $7, $8, $8)
	`, nextRunID, run.TaskID, workspaceArg, handoff.ToAgent, run.Model, run.Provider, string(metadata), now)
	if err != nil {
		return false, "", "", fmt.Errorf("create follow-on agent run: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_messages
		SET consumed_at = $1, consumed_by_run_id = $2
		WHERE id = $3 AND consumed_at IS NULL
	`, now, nextRunID, handoff.ID)
	if err != nil {
		return false, "", "", fmt.Errorf("mark handoff consumed: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, "", "", fmt.Errorf("check handoff consumption: %w", err)
	}
	if rows == 0 {
		return false, "", "", nil
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE tasks SET status = 'running', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, run.TaskID)
	if err != nil {
		return false, "", "", fmt.Errorf("update task for follow-on run: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, "", "", fmt.Errorf("commit follow-on run: %w", err)
	}

	if h.eventBus != nil {
		payload := map[string]any{
			"run_id":             nextRunID,
			"task_id":            run.TaskID,
			"agent_role":         handoff.ToAgent,
			"status":             "queued",
			"action":             "mailbox_handoff",
			"handoff_message_id": handoff.ID,
			"previous_run_id":    run.RunID,
		}
		data, _ := json.Marshal(payload)
		if err := h.eventBus.Publish(events.RunTriggered, data); err != nil {
			h.logger.Warn("failed to publish follow-on run triggered event", "error", err)
		}
	}

	return true, nextRunID, handoff.ToAgent, nil
}

func (h *RunHandler) loadCompletedRunContext(ctx context.Context, event events.AgentRunEvent) (*completedRunContext, error) {
	var workspaceID, model, provider, metadata sql.NullString
	run := &completedRunContext{RunID: event.RunID}
	err := h.db.QueryRowContext(ctx, `
		SELECT task_id, workspace_id, agent_role, model, provider, metadata
		FROM agent_runs
		WHERE id = $1
	`, event.RunID).Scan(&run.TaskID, &workspaceID, &run.AgentRole, &model, &provider, &metadata)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("completed run %s not found", event.RunID)
		}
		return nil, fmt.Errorf("load completed run context: %w", err)
	}
	if event.TaskID != "" && event.TaskID != run.TaskID {
		return nil, fmt.Errorf("completed run %s belongs to task %s, event referenced task %s", event.RunID, run.TaskID, event.TaskID)
	}
	if workspaceID.Valid {
		run.WorkspaceID = &workspaceID.String
	}
	run.Model = "gpt-4o"
	if model.Valid && strings.TrimSpace(model.String) != "" {
		run.Model = model.String
	}
	run.Provider = "openai"
	if provider.Valid && strings.TrimSpace(provider.String) != "" {
		run.Provider = provider.String
	}
	run.Metadata = "{}"
	if metadata.Valid && strings.TrimSpace(metadata.String) != "" {
		run.Metadata = metadata.String
	}
	return run, nil
}

func (h *RunHandler) nextUnconsumedHandoff(ctx context.Context, tx *sql.Tx, run *completedRunContext) (*pendingHandoff, error) {
	var handoff pendingHandoff
	err := tx.QueryRowContext(ctx, `
		SELECT id, to_agent
		FROM agent_messages
		WHERE task_id = $1
		  AND message_type = $2
		  AND consumed_at IS NULL
		  AND to_agent <> 'broadcast'
		  AND (agent_run_id = $3 OR agent_run_id IS NULL)
		ORDER BY created_at ASC
		LIMIT 1
	`, run.TaskID, models.MessageTypeHandoff, run.RunID).Scan(&handoff.ID, &handoff.ToAgent)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load next handoff: %w", err)
	}
	handoff.ToAgent = strings.TrimSpace(handoff.ToAgent)
	return &handoff, nil
}

func validAgentRole(role string) bool {
	switch role {
	case models.AgentRolePlanner,
		models.AgentRoleImplementer,
		models.AgentRoleReviewer,
		models.AgentRoleTestRunner,
		models.AgentRoleSecurity,
		models.AgentRoleDocs,
		models.AgentRoleReleaseManager:
		return true
	default:
		return false
	}
}

// HandleReviewCompleted processes review.completed events.
// 1. Request human approval for PR creation
func (h *RunHandler) HandleReviewCompleted(msg *nats.Msg) error {
	var payload struct {
		RunID     string `json:"run_id"`
		TaskID    string `json:"task_id"`
		Status    string `json:"status"`
		RiskLevel string `json:"risk_level"`
	}
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		return fmt.Errorf("unmarshal review completed event: %w", err)
	}

	h.logger.Info("handling review completed", "run_id", payload.RunID, "task_id", payload.TaskID)

	// Check if there's already a pending approval for this task
	var pendingCount int
	err := h.db.QueryRow(`
		SELECT COUNT(*) FROM approvals
		WHERE task_id = $1 AND response IS NULL
		AND (expires_at IS NULL OR expires_at > $2)
	`, payload.TaskID, time.Now().UTC()).Scan(&pendingCount)
	if err != nil {
		h.logger.Warn("failed to check pending approvals", "error", err)
	}
	if pendingCount > 0 {
		h.logger.Info("approval request already pending for task", "task_id", payload.TaskID)
		return ackMessage(msg)
	}

	// Create approval request for PR creation
	approvalID := uuid.New().String()
	now := time.Now().UTC()
	metadata := map[string]interface{}{
		"auto_created": true,
		"reason":       "review_completed",
		"run_id":       payload.RunID,
	}
	metadataJSON, _ := json.Marshal(metadata)

	_, err = h.db.Exec(`
		INSERT INTO approvals (
			id, task_id, agent_run_id, approval_type, requested_by,
			requested_at, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'system', $5, $6, $5, $5)
	`, approvalID, payload.TaskID, payload.RunID, models.ApprovalTypePRCreate, now, metadataJSON)
	if err != nil {
		return fmt.Errorf("create approval request: %w", err)
	}

	h.logger.Info("approval request created for PR creation",
		"approval_id", approvalID,
		"task_id", payload.TaskID,
		"run_id", payload.RunID,
	)

	return ackMessage(msg)
}

// HandleRunTriggered processes runs.triggered events by executing the queued
// agent run through the configured executor.
func (h *RunHandler) HandleRunTriggered(msg *nats.Msg) error {
	var event events.RunEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal run triggered event: %w", err)
	}
	if strings.TrimSpace(event.RunID) == "" {
		return fmt.Errorf("run triggered event missing run_id")
	}
	if h.executor == nil {
		return fmt.Errorf("run executor is not configured")
	}

	h.logger.Info("executing triggered run", "run_id", event.RunID, "task_id", event.TaskID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if h.admission != nil {
		decision, err := h.admission.AdmitRun(ctx, event.RunID, event.TaskID)
		if err != nil {
			return fmt.Errorf("admit run %s: %w", event.RunID, err)
		}
		if !decision.Allowed {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "scheduler admission denied"
			}
			retryAfter := decision.RetryAfter
			if retryAfter <= 0 {
				retryAfter = 5 * time.Second
			}
			if msg != nil && msg.Reply != "" {
				if nakErr := msg.NakWithDelay(retryAfter); nakErr != nil &&
					!errors.Is(nakErr, nats.ErrMsgNotBound) &&
					!errors.Is(nakErr, nats.ErrMsgNoReply) {
					return fmt.Errorf("defer run admission: %w", nakErr)
				}
			}
			return fmt.Errorf("%w: run %s not admitted: %s", ErrRunAdmissionDeferred, event.RunID, reason)
		}
	}
	err := h.executor.ExecuteRun(ctx, event.RunID)
	if h.admission != nil {
		if releaseErr := h.admission.ReleaseRun(ctx, event.RunID); releaseErr != nil {
			if err == nil {
				return fmt.Errorf("release run admission %s: %w", event.RunID, releaseErr)
			}
			h.logger.Warn("failed to release run admission after execution error", "run_id", event.RunID, "error", releaseErr)
		}
	}
	if err != nil {
		return fmt.Errorf("execute run %s: %w", event.RunID, err)
	}
	return ackMessage(msg)
}

func ackMessage(msg *nats.Msg) error {
	if msg == nil || msg.Reply == "" {
		return nil
	}
	if err := msg.Ack(); err != nil && err != nats.ErrMsgNoReply {
		return err
	}
	return nil
}
