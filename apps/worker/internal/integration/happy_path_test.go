package integration

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/api/pkg/agentexecutor"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/worker/internal/handlers"
)

// TestLiveModelProviderRun exercises the full implementer run using a real
// model provider and the Docker runtime provider. It verifies that:
//   - tasks.approved provisions a Docker workspace and creates an implementer run
//   - the run executes against the real model provider and completes
//   - the worker generates a review report after run completion
//
// Required environment:
//   - RUN_LIVE_E2E=1
//   - OPENAI_API_KEY, ANTHROPIC_API_KEY, or another supported provider key
//   - GITHUB_TOKEN, GITHUB_TEST_OWNER, GITHUB_TEST_REPO (for repo metadata)
//   - NATS running at NATS_URL (default nats://localhost:4222)
//   - Docker daemon available
func TestLiveModelProviderRun(t *testing.T) {
	env := newTestEnv(t)

	// This test clones a real GitHub repository into a Docker workspace and
	// then executes against a live model provider.
	skipWithoutGitHub(t)

	// Validate that at least one model provider key is available.
	if os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" &&
		os.Getenv("GEMINI_API_KEY") == "" && os.Getenv("GROQ_API_KEY") == "" &&
		os.Getenv("FIREWORKS_API_KEY") == "" {
		t.Skip("set at least one model provider API key (OPENAI_API_KEY, ANTHROPIC_API_KEY, etc.)")
	}

	baseDir := t.TempDir()
	env.setupRuntime("docker", baseDir)

	// Use a permissive policy engine so the live model can write files, run
	// commands, and create commits without triggering approval pauses.
	policyEngine := policies.NewEngine([]policies.Policy{
		{Name: "allow_all", ResourceType: "*", Action: "*", Effect: policies.EffectAllow, Priority: 1000},
	})
	executor := agentexecutor.New(env.db.DB, env.eventBus, env.logger).
		WithRuntimeProvider(env.runtimeName, env.runtime).
		WithPolicyEngine(policyEngine)
	env.taskHandler = handlers.NewTaskHandler(env.db.DB, env.logger).
		WithEventPublisher(env.eventBus).
		WithRuntimeProvider(env.runtime, env.runtimeName)

	reviewService := reviewer.NewReviewer(env.db.DB, env.logger)
	env.runHandler = handlers.NewRunHandler(env.db.DB, env.logger, env.eventBus).
		WithRunExecutor(executor).
		WithReviewer(reviewService)
	env.approvalHandler = handlers.NewApprovalHandler(env.db.DB, env.logger, env.eventBus)
	env.startSubscriptions()

	spec := json.RawMessage(`{
		"summary": "Add a one-line note to README.md describing this automated test run.",
		"plan": [
			{"step": 1, "action": "read README.md"},
			{"step": 2, "action": "append a short test note to README.md"}
		],
		"acceptance_criteria": ["README.md contains a new line mentioning the live end-to-end test"]
	}`)

	taskID := env.createTask("spec_review", spec)
	env.approveTask(taskID)

	env.waitForCondition("workspace created", 2*time.Minute, func() bool {
		var count int
		err := env.db.DB.QueryRow(`SELECT COUNT(*) FROM workspaces WHERE task_id = $1`, taskID).Scan(&count)
		return err == nil && count == 1
	})

	runID := env.latestRunID(taskID)
	t.Logf("created run %s for task %s", runID, taskID)

	env.waitForCondition("agent run completed", 5*time.Minute, func() bool {
		return env.runStatus(runID) == "completed"
	})

	if count := env.stepCount(runID); count == 0 {
		t.Fatalf("expected agent steps for run %s, got none", runID)
	}
	t.Logf("run %s completed with %d steps", runID, env.stepCount(runID))

	env.waitForCondition("review report generated", 2*time.Minute, func() bool {
		var count int
		err := env.db.DB.QueryRow(`SELECT COUNT(*) FROM review_reports WHERE run_id = $1`, runID).Scan(&count)
		return err == nil && count == 1
	})

	t.Logf("live model-provider run completed successfully for task %s", taskID)
}

// TestLivePRCreation verifies that approving a PR creation approval request
// results in a branch push, a GitHub PR, and a persisted pull_requests row.
// This test uses the local runtime provider to avoid the current limitation
// that prfactory requires a local worktree path.
func TestLivePRCreation(t *testing.T) {
	env := newTestEnv(t)
	skipWithoutGitHub(t)

	baseDir := t.TempDir()
	env.setupRuntime("local", baseDir)
	env.setupHandlers()
	env.startSubscriptions()

	now := time.Now().UTC()
	taskID := env.createTask("reviewing", nil)
	workspaceID := env.seedLocalWorkspace(taskID, baseDir, "live-e2e-test-change")
	runID := env.seedCompletedRun(taskID, workspaceID, now)
	env.seedReviewReport(runID, "low", true)

	// Trigger the review.completed handler to create a PR approval request.
	env.publishEvent(events.ReviewCompleted, map[string]any{
		"run_id":     runID,
		"task_id":    taskID,
		"status":     "completed",
		"risk_level": "low",
		"approvable": true,
	})

	var approvalID string
	env.waitForCondition("PR approval request created", 30*time.Second, func() bool {
		var count int
		err := env.db.DB.QueryRow(`
			SELECT COUNT(*) FROM approvals WHERE task_id = $1 AND approval_type = 'pr_create'
		`, taskID).Scan(&count)
		if err != nil || count == 0 {
			return false
		}
		approvalID, _ = env.pendingApproval(taskID)
		return true
	})

	// Approve PR creation.
	env.respondApproval(approvalID)

	env.waitForCondition("PR record created", 2*time.Minute, func() bool {
		var count int
		err := env.db.DB.QueryRow(`
			SELECT COUNT(*) FROM pull_requests WHERE task_id = $1
		`, taskID).Scan(&count)
		return err == nil && count == 1
	})

	var prURL string
	err := env.db.DB.QueryRow(`SELECT url FROM pull_requests WHERE task_id = $1`, taskID).Scan(&prURL)
	if err != nil {
		t.Fatalf("query PR URL: %v", err)
	}
	if prURL == "" {
		t.Fatalf("expected a GitHub PR URL, got empty")
	}

	t.Logf("live PR created: %s", prURL)
}
