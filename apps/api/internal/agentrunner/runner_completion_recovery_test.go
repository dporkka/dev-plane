package agentrunner

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/models"
)

func TestCompleteRunWithRecoveryRetriesTemporaryPersistenceFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("UPDATE agent_runs").WithArgs("completed", "done", 1.25, sqlmock.AnyArg(), "run-1").WillReturnError(errors.New("temporary database timeout"))
	mock.ExpectExec("UPDATE agent_runs").WithArgs("completed", "done", 1.25, sqlmock.AnyArg(), "run-1").WillReturnResult(sqlmock.NewResult(0, 1))
	runner := &Runner{db: db, logger: slog.Default()}
	if err := runner.completeRunWithRecovery(context.Background(), "run-1", "done", &RunState{CostSoFar: 1.25}); err != nil {
		t.Fatalf("temporary database failure must recover: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRunWithRecoveryDoesNotOverwriteCancellation(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("UPDATE agent_runs").WithArgs("completed", "done", 0.0, sqlmock.AnyArg(), "run-1").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT status, summary, total_cost FROM agent_runs").WithArgs("run-1").WillReturnRows(sqlmock.NewRows([]string{"status", "summary", "total_cost"}).AddRow(models.AgentRunStatusCancelled, "", 0.0))
	runner := &Runner{db: db, logger: slog.Default()}
	err = runner.completeRunWithRecovery(context.Background(), "run-1", "done", &RunState{})
	if !errors.Is(err, ErrRunTerminalStateChanged) {
		t.Fatalf("want terminal state conflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRunWithRecoveryFailsPersistentlyUnavailableDatabaseWithoutInventedTerminalState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 3; i++ {
		mock.ExpectExec("UPDATE agent_runs").WithArgs("completed", "done", 0.0, sqlmock.AnyArg(), "run-1").WillReturnError(errors.New("database unavailable"))
	}
	mock.ExpectExec("UPDATE agent_runs").WithArgs("failed", sqlmock.AnyArg(), sqlmock.AnyArg(), "run-1").WillReturnError(errors.New("database still unavailable"))
	runner := &Runner{db: db, logger: slog.Default()}
	err = runner.completeRunWithRecovery(context.Background(), "run-1", "done", &RunState{})
	if err == nil || !strings.Contains(err.Error(), "database unavailable") || !strings.Contains(err.Error(), "database still unavailable") {
		t.Fatalf("must report original and fallback failures; got %v", err)
	}
	if e := mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestCompleteRunWithRecoveryPersistsFailedStatusIfCompletionCannotBeSaved(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 3; i++ {
		mock.ExpectExec("UPDATE agent_runs").WithArgs("completed", "done", 0.0, sqlmock.AnyArg(), "run-1").WillReturnError(errors.New("completion write rejected"))
	}
	mock.ExpectExec("UPDATE agent_runs").WithArgs("failed", sqlmock.AnyArg(), sqlmock.AnyArg(), "run-1").WillReturnResult(sqlmock.NewResult(0, 1))
	runner := &Runner{db: db, logger: slog.Default()}
	err = runner.completeRunWithRecovery(context.Background(), "run-1", "done", &RunState{})
	if err == nil || !strings.Contains(err.Error(), "completion write rejected") {
		t.Fatalf("must report original completion error: %v", err)
	}
	if e := mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestUpdateRunCompletionRecognizesPriorCommittedAttempt(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("UPDATE agent_runs").WithArgs("completed", "done", 0.5, sqlmock.AnyArg(), "run-1").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT status, summary, total_cost FROM agent_runs").WithArgs("run-1").WillReturnRows(sqlmock.NewRows([]string{"status", "summary", "total_cost"}).AddRow("completed", "done", 0.5))
	runner := &Runner{db: db, logger: slog.Default()}
	if err := runner.updateRunCompletion(context.Background(), "run-1", "completed", "done", &RunState{CostSoFar: 0.5}); err != nil {
		t.Fatalf("must recognize already persisted completion: %v", err)
	}
	if e := mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestFailRunDoesNotClaimTerminalStateWhenPersistenceFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("UPDATE agent_runs").WithArgs("failed", "tool failed", sqlmock.AnyArg(), "run-1").WillReturnError(errors.New("database unavailable"))
	runner := &Runner{db: db, logger: slog.Default()}
	err = runner.failRun(context.Background(), "run-1", "tool failed")
	if err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("must report persistence uncertainty, got %v", err)
	}
	if e := mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestFailRunDoesNotOverwriteCompletedOrCancelledRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("UPDATE agent_runs").WithArgs("failed", "late failure", sqlmock.AnyArg(), "run-1").WillReturnResult(sqlmock.NewResult(0, 0))
	runner := &Runner{db: db, logger: slog.Default()}
	err = runner.failRun(context.Background(), "run-1", "late failure")
	if !errors.Is(err, ErrRunTerminalStateChanged) {
		t.Fatalf("late failure must preserve previous terminal status: %v", err)
	}
	if e := mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

// Real SQLite state confirms completion-write failures do not strand an otherwise
// completed agent run in running when the database can accept a failed state.
func TestRunCompletionWriteFailurePersistsRecoverableFailedRun(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()
	insertRunnerFixture(t, db, t.TempDir(), models.AgentRoleImplementer)
	_, err := db.Exec(`
        CREATE TRIGGER reject_completed_run
        BEFORE UPDATE OF status ON agent_runs
        WHEN NEW.status = 'completed'
        BEGIN SELECT RAISE(ABORT, 'completion write rejected'); END;
    `)
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeModelProvider{responses: []string{`{"action":"final_response","content":"Work finished"}`}}
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))
	err = runner.Run(context.Background(), "run-1")
	if err == nil || !strings.Contains(err.Error(), "completion write rejected") {
		t.Fatalf("must report rejected completion persistence, got %v", err)
	}
	var status, failure string
	if err := db.QueryRow(`SELECT status, error_message FROM agent_runs WHERE id = 'run-1'`).Scan(&status, &failure); err != nil {
		t.Fatal(err)
	}
	if status != models.AgentRunStatusFailed || !strings.Contains(failure, "manual recovery required") {
		t.Fatalf("run status = %q, failure = %q; want durable recoverable failure", status, failure)
	}
}

func TestRunStartDoesNotResurrectAlreadyCancelledOrRunningRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("UPDATE agent_runs").WithArgs("running", sqlmock.AnyArg(), sqlmock.AnyArg(), "run-1").WillReturnResult(sqlmock.NewResult(0, 0))
	runner := &Runner{db: db, logger: slog.Default()}
	err = runner.updateRunStatus(context.Background(), "run-1", "running", nil)
	if !errors.Is(err, ErrRunTerminalStateChanged) {
		t.Fatalf("duplicate or canceled run start must fail closed: %v", err)
	}
	if e := mock.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
