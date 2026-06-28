package integration

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/api/pkg/agentexecutor"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/worker/internal/handlers"
)

// TestLiveResumeAfterApproval verifies that a paused implementer run is
// resumed when a human approves the model-requested approval. It uses a
// deterministic model provider so no live model credentials are required.
func TestLiveResumeAfterApproval(t *testing.T) {
	env := newTestEnv(t)

	baseDir := t.TempDir()
	env.setupRuntime("local", baseDir)

	// Use a permissive policy engine so the deterministic "agent" can finish
	// without triggering additional approval requests.
	policyEngine := policies.NewEngine([]policies.Policy{
		{Name: "allow_all", ResourceType: "*", Action: "*", Effect: policies.EffectAllow, Priority: 1000},
	})

	// Build a custom executor with deterministic responses:
	//   1. request_approval -> pauses the run
	//   2. final_response -> completes the run after resume
	executor := agentexecutor.New(env.db.DB, env.eventBus, env.logger).
		WithRuntimeProvider(env.runtimeName, env.runtime).
		WithPolicyEngine(policyEngine).
		WithDeterministicResponses(
			`{"action":"request_approval","content":"Please approve continuing this test run."}`,
			`{"action":"final_response","content":"Test run resumed and completed successfully."}`,
		)

	reviewService := reviewer.NewReviewer(env.db.DB, env.logger)
	env.runHandler = handlers.NewRunHandler(env.db.DB, env.logger, env.eventBus).
		WithRunExecutor(executor).
		WithReviewer(reviewService)
	env.approvalHandler = handlers.NewApprovalHandler(env.db.DB, env.logger, env.eventBus)
	env.startSubscriptions()

	taskID := env.createTask("running", nil)
	workspaceID := env.seedFakeLocalWorkspace(taskID, baseDir, "resume-test")
	runID := env.seedQueuedRun(taskID, workspaceID)

	// Trigger the run.
	env.publishEvent(events.RunTriggered, map[string]any{
		"run_id":  runID,
		"task_id": taskID,
		"status":  "queued",
		"action":  "resume_test",
	})

	// Wait for the run to pause with an approval request.
	env.waitForCondition("run paused with approval request", 30*time.Second, func() bool {
		return env.runStatus(runID) == "paused"
	})

	var approvalID string
	env.waitForCondition("approval request created", 10*time.Second, func() bool {
		var count int
		err := env.db.DB.QueryRow(`
			SELECT COUNT(*) FROM approvals WHERE task_id = $1 AND approval_type = 'risky_action'
		`, taskID).Scan(&count)
		if err != nil || count == 0 {
			return false
		}
		approvalID, _ = env.pendingApproval(taskID)
		return approvalID != ""
	})

	firstMaxStep := env.maxStepNumber(runID)
	if firstMaxStep < 1 {
		t.Fatalf("expected at least one step before pause, got %d", firstMaxStep)
	}

	// Approve the request; worker should requeue the run and trigger it again.
	env.respondApproval(approvalID)

	env.waitForCondition("run completed after resume", 30*time.Second, func() bool {
		return env.runStatus(runID) == "completed"
	})

	// Verify step numbering continued from the prior max.
	secondMaxStep := env.maxStepNumber(runID)
	if secondMaxStep <= firstMaxStep {
		t.Fatalf("expected step numbering to continue after resume (%d > %d)", secondMaxStep, firstMaxStep)
	}

	t.Logf("run %s resumed and completed; steps before pause=%d, total steps=%d", runID, firstMaxStep, secondMaxStep)
}

// seedQueuedRun inserts a queued implementer run for a task/workspace.
func (env *testEnv) seedQueuedRun(taskID, workspaceID string) string {
	runID := uuid.New().String()
	now := time.Now().UTC()
	mustExec(env.t, env.db.DB, `
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider,
			status, total_cost, metadata, created_at, updated_at
		) VALUES ($1, $2, $3, 'implementer', 'gpt-4o', 'openai', 'queued',
			0.0, '{}', $4, $4)
	`, runID, taskID, workspaceID, now)
	return runID
}
