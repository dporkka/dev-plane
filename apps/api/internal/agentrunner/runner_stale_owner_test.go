package agentrunner

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/ai-dev-control-plane/execution"
	"github.com/ai-dev-control-plane/models"
)

func TestStaleWorkerCannotPersistFailedOrCompletedRun(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()
	insertRunnerFixture(t, db, t.TempDir(), models.AgentRoleImplementer)
	ctx := execution.WithEffectGuard(context.Background(), func(context.Context) error { return execution.ErrStaleEpoch })
	runner := &Runner{db: db, logger: slog.Default()}
	err := runner.failRun(ctx, "run-1", "stale tool error")
	if !errors.Is(err, execution.ErrStaleEpoch) {
		t.Fatalf("stale worker failure transition not rejected: %v", err)
	}
	err = runner.updateRunCompletion(ctx, "run-1", models.AgentRunStatusCompleted, "false success", &RunState{})
	if !errors.Is(err, execution.ErrStaleEpoch) {
		t.Fatalf("stale worker completion transition not rejected: %v", err)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM agent_runs WHERE id='run-1'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == models.AgentRunStatusFailed || status == models.AgentRunStatusCompleted {
		t.Fatalf("stale worker changed run state to %q", status)
	}
}
