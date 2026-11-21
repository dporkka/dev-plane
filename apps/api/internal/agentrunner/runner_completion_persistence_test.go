package agentrunner

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/models"
)

func TestRunDoesNotReportSuccessIfCompletionPersistenceFails(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()
	insertRunnerFixture(t, db, t.TempDir(), models.AgentRoleImplementer)
	if _, err := db.Exec(`
       CREATE TRIGGER deny_completion
       BEFORE UPDATE OF status ON agent_runs
       WHEN NEW.status='completed'
       BEGIN SELECT RAISE(ABORT,'completion write rejected'); END;
    `); err != nil {
		t.Fatal(err)
	}
	provider := &fakeModelProvider{responses: []string{`{"action":"final_response","content":"done"}`}}
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))
	err := runner.Run(context.Background(), "run-1")
	if err == nil || !strings.Contains(err.Error(), "completion write rejected") {
		t.Fatalf("runner falsely reported completion even though DB rejected it: %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id='run-1'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == models.AgentRunStatusCompleted {
		t.Fatal("run was not durably completed")
	}
}
