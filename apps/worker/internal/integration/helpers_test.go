// Package integration contains credential-gated live end-to-end tests for the
// worker service. These tests exercise the full task lifecycle against real
// NATS, Docker, model providers, and GitHub.
//
// They are skipped unless RUN_LIVE_E2E=1 is set in the environment.
package integration

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/api/pkg/agentexecutor"
	"github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/runtimes"
	"github.com/ai-dev-control-plane/worker/internal/handlers"
)

const (
	envRunLiveE2E        = "RUN_LIVE_E2E"
	envModelProvider     = "LIVE_E2E_MODEL_PROVIDER"
	envGitHubToken       = "GITHUB_TOKEN"
	envGitHubTestOwner   = "GITHUB_TEST_OWNER"
	envGitHubTestRepo    = "GITHUB_TEST_REPO"
	envNATSURL           = "NATS_URL"
	envWorkspaceRuntime  = "WORKSPACE_RUNTIME"
	envWorkspaceBaseDir  = "WORKSPACE_BASE_DIR"
)

// testEnv holds shared test infrastructure.
type testEnv struct {
	t            *testing.T
	db           *db.DB
	dbPath       string
	eventBus     *events.Bus
	logger       *slog.Logger
	runtime      runtimes.Provider
	runtimeName  string
	taskHandler  *handlers.TaskHandler
	runHandler   *handlers.RunHandler
	approvalHandler *handlers.ApprovalHandler
	orgID        string
	projectID    string
	repoID       string
	userID       string
}

// findMigrationsDir locates packages/db/migrations relative to this test file.
func findMigrationsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot determine test file location")
	}
	// This file is in apps/worker/internal/integration.
	// packages/db/migrations is at repo-root/packages/db/migrations.
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..", "..", "..")
	return filepath.Join(repoRoot, "packages", "db", "migrations")
}

// skipIfDisabled skips the test unless RUN_LIVE_E2E=1.
func skipIfDisabled(t *testing.T) {
	if os.Getenv(envRunLiveE2E) != "1" {
		t.Skipf("set %s=1 to run live end-to-end integration tests", envRunLiveE2E)
	}
}

// requireEnv fails the test if a required environment variable is missing.
func requireEnv(t *testing.T, key string) string {
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("required environment variable %s is not set", key)
	}
	return v
}

// newTestEnv creates a fresh test environment with an isolated database,
// NATS connection, and seeded organization/project/repository.
func newTestEnv(t *testing.T) *testEnv {
	skipIfDisabled(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	dbPath := filepath.Join(t.TempDir(), "live-e2e.db")
	database, err := db.New("file:" + dbPath)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	migrationsDir := findMigrationsDir(t)
	if err := database.RunMigrations(migrationsDir); err != nil {
		t.Fatalf("run migrations from %s: %v", migrationsDir, err)
	}

	natsURL := os.Getenv(envNATSURL)
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}
	eventBus, err := events.New(natsURL)
	if err != nil {
		t.Fatalf("connect to NATS at %s: %v", natsURL, err)
	}
	t.Cleanup(func() { eventBus.Close() })
	if err := eventBus.CreateStreams(); err != nil {
		t.Fatalf("create NATS streams: %v", err)
	}

	// Purge streams so stale messages from prior failed/aborted runs are not
	// redelivered to durable consumers created by this test.
	for _, stream := range []string{events.StreamTasks, events.StreamAgents, events.StreamRuns, events.StreamWebhooks, events.StreamAudit} {
		if err := eventBus.JetStream().PurgeStream(stream); err != nil {
			t.Logf("purge stream %s: %v", stream, err)
		}
	}

	env := &testEnv{
		t:        t,
		db:       database,
		dbPath:   dbPath,
		eventBus: eventBus,
		logger:   logger,
		orgID:    uuid.New().String(),
		projectID: uuid.New().String(),
		repoID:   uuid.New().String(),
		userID:   "live-e2e-user",
	}

	env.seedOrgProjectRepo()

	return env
}

// setupRuntime configures the workspace runtime for the test.
func (env *testEnv) setupRuntime(runtimeName, baseDir string) {
	switch strings.ToLower(runtimeName) {
	case "docker":
		provider, err := runtimes.NewDockerProvider(baseDir)
		if err != nil {
			env.t.Fatalf("create docker provider: %v", err)
		}
		env.runtime = provider
		env.runtimeName = "docker"
	case "local", "":
		env.runtime = runtimes.NewLocalProvider(baseDir)
		env.runtimeName = "local"
	default:
		env.t.Fatalf("unsupported runtime %q", runtimeName)
	}
}

// setupHandlers constructs the worker handlers. Call after setupRuntime if a
// runtime provider is required.
func (env *testEnv) setupHandlers() {
	env.taskHandler = handlers.NewTaskHandler(env.db.DB, env.logger).
		WithEventPublisher(env.eventBus).
		WithRuntimeProvider(env.runtime, env.runtimeName)

	executor := agentexecutor.New(env.db.DB, env.eventBus, env.logger).
		WithRuntimeProvider(env.runtimeName, env.runtime)

	reviewService := reviewer.NewReviewer(env.db.DB, env.logger)
	env.runHandler = handlers.NewRunHandler(env.db.DB, env.logger, env.eventBus).
		WithRunExecutor(executor).
		WithReviewer(reviewService)

	env.approvalHandler = handlers.NewApprovalHandler(env.db.DB, env.logger, env.eventBus)
}

// startSubscriptions wires the worker handlers to NATS subjects.
func (env *testEnv) startSubscriptions() {
	subjects := map[string]func(*nats.Msg) error{
		events.TaskApproved:        env.taskHandler.HandleTaskApproved,
		events.AgentRunCompleted:   env.runHandler.HandleRunCompleted,
		events.ReviewCompleted:     env.runHandler.HandleReviewCompleted,
		events.RunTriggered:        env.runHandler.HandleRunTriggered,
		events.ApprovalApproved:    env.approvalHandler.HandleApprovalApproved,
	}

	for subject, handler := range subjects {
		subject := subject
		handler := handler
		_, err := env.eventBus.Subscribe(subject, func(msg *nats.Msg) {
			if err := handler(msg); err != nil {
				env.logger.Error("handler error", "subject", subject, "error", err)
			}
		})
		if err != nil {
			env.t.Fatalf("subscribe to %s: %v", subject, err)
		}
	}
}

// seedOrgProjectRepo inserts the minimum org/project/repository rows. It uses
// GITHUB_TEST_OWNER/GITHUB_TEST_REPO when available so tests that talk to
// GitHub have matching metadata; otherwise it falls back to placeholder values
// for tests that only need a repository row.
func (env *testEnv) seedOrgProjectRepo() {
	now := time.Now().UTC()
	owner := os.Getenv(envGitHubTestOwner)
	if owner == "" {
		owner = "ai-dev-control-plane"
	}
	name := os.Getenv(envGitHubTestRepo)
	if name == "" {
		name = "live-e2e-fixture"
	}
	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, name)

	mustExec(env.t, env.db.DB, `
		INSERT INTO organizations (id, name, slug, created_at, updated_at)
		VALUES ($1, 'Live E2E Org', 'live-e2e-org', $2, $2)
	`, env.orgID, now)

	mustExec(env.t, env.db.DB, `
		INSERT INTO users (id, organization_id, email, name, role, created_at, updated_at)
		VALUES ($1, $2, 'live-e2e@dev-plane.local', 'Live E2E User', 'owner', $3, $3)
	`, env.userID, env.orgID, now)

	mustExec(env.t, env.db.DB, `
		INSERT INTO projects (id, organization_id, name, slug, description, created_at, updated_at)
		VALUES ($1, $2, 'Live E2E Project', 'live-e2e-project', 'disposable project for live e2e tests', $3, $3)
	`, env.projectID, env.orgID, now)

	mustExec(env.t, env.db.DB, `
		INSERT INTO repositories (
			id, project_id, github_id, owner, name, full_name, clone_url,
			default_branch, private, connection_status, settings, created_at, updated_at
		) VALUES ($1, $2, 0, $3, $4, $5, $6, 'main', false, 'connected', '{}', $7, $7)
	`, env.repoID, env.projectID, owner, name, owner+"/"+name, cloneURL, now)
}

// skipWithoutGitHub skips the current test unless the GitHub credentials
// needed to clone a repo and open a PR are present.
func skipWithoutGitHub(t *testing.T) {
	if os.Getenv(envGitHubToken) == "" {
		t.Skipf("set %s to run this live GitHub gate", envGitHubToken)
	}
	if os.Getenv(envGitHubTestOwner) == "" {
		t.Skipf("set %s to run this live GitHub gate", envGitHubTestOwner)
	}
	if os.Getenv(envGitHubTestRepo) == "" {
		t.Skipf("set %s to run this live GitHub gate", envGitHubTestRepo)
	}
}

// createTask inserts a task in the given status with an optional spec.
func (env *testEnv) createTask(status string, spec json.RawMessage) string {
	now := time.Now().UTC()
	id := uuid.New().String()
	var specArg any
	if len(spec) > 0 {
		specArg = spec
	}
	mustExec(env.t, env.db.DB, `
		INSERT INTO tasks (
			id, project_id, repository_id, created_by, source, title,
			description, status, priority, risk_level, target_branch, spec,
			acceptance_criteria, max_runtime_minutes, approval_requirements, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, 'live_e2e', 'Live E2E Task', 'Automated live end-to-end test task',
			$5, 'medium', 'low', 'main', $6, '[]', 60, '[]', '{}', $7, $7)
	`, id, env.projectID, env.repoID, env.userID, status, specArg, now)
	return id
}

// publishEvent publishes an event to NATS.
func (env *testEnv) publishEvent(subject string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		env.t.Fatalf("marshal %s payload: %v", subject, err)
	}
	if err := env.eventBus.Publish(subject, data); err != nil {
		env.t.Fatalf("publish %s: %v", subject, err)
	}
}

// approveTask transitions a task from spec_review to approved and publishes
// tasks.approved.
func (env *testEnv) approveTask(taskID string) {
	now := time.Now().UTC()
	mustExec(env.t, env.db.DB, `
		UPDATE tasks SET status = 'approved', updated_at = $1 WHERE id = $2
	`, now, taskID)
	env.publishEvent(events.TaskApproved, events.TaskEvent{TaskID: taskID, Status: "approved"})
}

// respondApproval updates an approval row and publishes approval.approved.
func (env *testEnv) respondApproval(approvalID string) {
	var taskID, approvalType string
	var agentRunID sql.NullString
	err := env.db.DB.QueryRow(`
		SELECT task_id, agent_run_id, approval_type FROM approvals WHERE id = $1
	`, approvalID).Scan(&taskID, &agentRunID, &approvalType)
	if err != nil {
		env.t.Fatalf("load approval details: %v", err)
	}

	now := time.Now().UTC()
	mustExec(env.t, env.db.DB, `
		UPDATE approvals SET response = 'approved', responded_by = $1, responded_at = $2, updated_at = $2
		WHERE id = $3
	`, env.userID, now, approvalID)

	payload := map[string]any{
		"approval_id":   approvalID,
		"task_id":       taskID,
		"response":      "approved",
		"responder_id":  env.userID,
		"approval_type": approvalType,
	}
	if agentRunID.Valid {
		payload["agent_run_id"] = agentRunID.String
	}
	env.publishEvent(events.ApprovalApproved, payload)
}

// waitForCondition polls the DB until the predicate returns true or timeout.
func (env *testEnv) waitForCondition(name string, timeout time.Duration, predicate func() bool) {
	env.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	env.t.Fatalf("timed out waiting for condition: %s", name)
}

// taskStatus returns the current task status.
func (env *testEnv) taskStatus(taskID string) string {
	var status string
	err := env.db.DB.QueryRow(`SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status)
	if err != nil {
		env.t.Fatalf("query task status: %v", err)
	}
	return status
}

// runStatus returns the current agent run status.
func (env *testEnv) runStatus(runID string) string {
	var status string
	err := env.db.DB.QueryRow(`SELECT status FROM agent_runs WHERE id = $1`, runID).Scan(&status)
	if err != nil {
		env.t.Fatalf("query run status: %v", err)
	}
	return status
}

// latestRunID returns the most recent agent run ID for a task.
func (env *testEnv) latestRunID(taskID string) string {
	var id string
	err := env.db.DB.QueryRow(`
		SELECT id FROM agent_runs WHERE task_id = $1 ORDER BY created_at DESC LIMIT 1
	`, taskID).Scan(&id)
	if err != nil {
		env.t.Fatalf("query latest run: %v", err)
	}
	return id
}

// pendingApproval returns the first pending approval for a task.
func (env *testEnv) pendingApproval(taskID string) (string, string) {
	var id, approvalType string
	err := env.db.DB.QueryRow(`
		SELECT id, approval_type FROM approvals
		WHERE task_id = $1 AND response IS NULL
		ORDER BY requested_at ASC LIMIT 1
	`, taskID).Scan(&id, &approvalType)
	if err != nil {
		env.t.Fatalf("query pending approval: %v", err)
	}
	return id, approvalType
}

// stepCount returns the number of agent steps for a run.
func (env *testEnv) stepCount(runID string) int {
	var count int
	err := env.db.DB.QueryRow(`SELECT COUNT(*) FROM agent_steps WHERE agent_run_id = $1`, runID).Scan(&count)
	if err != nil {
		env.t.Fatalf("query step count: %v", err)
	}
	return count
}

// maxStepNumber returns the highest step number for a run.
func (env *testEnv) maxStepNumber(runID string) int {
	var max sql.NullInt32
	err := env.db.DB.QueryRow(`SELECT MAX(step_number) FROM agent_steps WHERE agent_run_id = $1`, runID).Scan(&max)
	if err != nil {
		env.t.Fatalf("query max step number: %v", err)
	}
	if !max.Valid {
		return 0
	}
	return int(max.Int32)
}

// seedFakeLocalWorkspace creates a local workspace directory and inserts a
// workspaces row without cloning from GitHub. Use for tests that do not
// exercise git operations through the runtime provider.
func (env *testEnv) seedFakeLocalWorkspace(taskID, baseDir, marker string) string {
	workspaceID := uuid.New().String()
	repoDir := filepath.Join(baseDir, workspaceID)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		env.t.Fatalf("create fake workspace dir: %v", err)
	}
	markerFile := filepath.Join(repoDir, "live-e2e-marker.md")
	if err := os.WriteFile(markerFile, []byte("# Live E2E Test\n\n"+marker+"\n"), 0644); err != nil {
		env.t.Fatalf("write marker file: %v", err)
	}

	now := time.Now().UTC()
	branch := "agent/live-e2e-" + workspaceID[:8]
	mustExec(env.t, env.db.DB, `
		INSERT INTO workspaces (
			id, repository_id, task_id, name, branch, base_branch,
			worktree_path, runtime_provider, runtime_session_id, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'main', $6, 'local', NULL, 'ready', $7, $7)
	`, workspaceID, env.repoID, taskID, "workspace-"+workspaceID[:8], branch, repoDir, now)
	mustExec(env.t, env.db.DB, `UPDATE tasks SET workspace_id = $1 WHERE id = $2`, workspaceID, taskID)
	return workspaceID
}

// seedLocalWorkspace clones the configured GitHub repository into baseDir,
// creates a workspace branch, makes a commit with the given marker, and
// inserts a workspaces row. It returns the workspace ID.
func (env *testEnv) seedLocalWorkspace(taskID, baseDir, marker string) string {
	workspaceID := uuid.New().String()
	repoDir := filepath.Join(baseDir, workspaceID)
	owner := requireEnv(env.t, envGitHubTestOwner)
	name := requireEnv(env.t, envGitHubTestRepo)

	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, name)
	if token := os.Getenv(envGitHubToken); token != "" {
		// Use the token for cloning private repositories.
		cloneURL = fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", token, owner, name)
	}

	if out, err := runCmd(env.t, "git", "clone", cloneURL, repoDir); err != nil {
		env.t.Fatalf("clone test repo: %v: %s", err, out)
	}

	branch := "agent/live-e2e-" + workspaceID[:8]
	if out, err := runCmd(env.t, "git", "-C", repoDir, "checkout", "-B", branch, "origin/main"); err != nil {
		env.t.Fatalf("create workspace branch: %v: %s", err, out)
	}

	markerFile := filepath.Join(repoDir, "live-e2e-marker.md")
	if err := os.WriteFile(markerFile, []byte("# Live E2E Test\n\n"+marker+"\n"), 0644); err != nil {
		env.t.Fatalf("write marker file: %v", err)
	}

	if out, err := runCmd(env.t, "git", "-C", repoDir, "add", "."); err != nil {
		env.t.Fatalf("git add: %v: %s", err, out)
	}
	if out, err := runCmd(env.t, "git", "-C", repoDir, "-c", "user.email=live-e2e@dev-plane.local", "-c", "user.name=Live E2E", "commit", "-m", "Live E2E test change"); err != nil {
		env.t.Fatalf("git commit: %v: %s", err, out)
	}

	now := time.Now().UTC()
	mustExec(env.t, env.db.DB, `
		INSERT INTO workspaces (
			id, repository_id, task_id, name, branch, base_branch,
			worktree_path, runtime_provider, runtime_session_id, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'main', $6, 'local', NULL, 'ready', $7, $7)
	`, workspaceID, env.repoID, taskID, "workspace-"+workspaceID[:8], branch, repoDir, now)

	// Link task to workspace.
	mustExec(env.t, env.db.DB, `UPDATE tasks SET workspace_id = $1 WHERE id = $2`, workspaceID, taskID)

	return workspaceID
}

// seedCompletedRun inserts a completed implementer run for a task/workspace.
func (env *testEnv) seedCompletedRun(taskID, workspaceID string, completedAt time.Time) string {
	runID := uuid.New().String()
	mustExec(env.t, env.db.DB, `
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider,
			status, total_cost, metadata, created_at, updated_at, completed_at
		) VALUES ($1, $2, $3, 'implementer', 'gpt-4o', 'openai', 'completed',
			0.0, '{}', $4, $4, $4)
	`, runID, taskID, workspaceID, completedAt)
	return runID
}

// seedReviewReport inserts a review report for a run.
func (env *testEnv) seedReviewReport(runID, riskLevel string, approvable bool) {
	now := time.Now().UTC()
	findingsJSON, _ := json.Marshal([]any{})
	suggestionsJSON, _ := json.Marshal([]any{})
	diffSummaryJSON, _ := json.Marshal(map[string]any{
		"files_changed": 1,
		"insertions":    3,
		"deletions":     0,
		"files": []any{
			map[string]any{
				"path":         "live-e2e-marker.md",
				"status":       "added",
				"insertions":   3,
				"deletions":    0,
				"is_test":      false,
				"is_config":    false,
				"is_migration": false,
			},
		},
	})
	mustExec(env.t, env.db.DB, `
		INSERT INTO review_reports (
			id, run_id, summary, findings, risk_level, approvable,
			suggestions, test_coverage, security_notes, diff_summary, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, uuid.New().String(), runID, "Live E2E review report.", findingsJSON, riskLevel, approvable,
		suggestionsJSON, "N/A", "No scanners run in local E2E test.", diffSummaryJSON, now)
}

// runCmd runs a command and returns combined output.
func runCmd(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// mustExec is a helper that fails the test on SQL error.
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec query: %v", err)
	}
}
