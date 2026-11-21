package agentrunner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/models"
)

func TestCompletionWriteRetriesTransientDatabaseFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("UPDATE agent_runs").WillReturnError(errors.New("transient unavailable"))
	mock.ExpectExec("UPDATE agent_runs").WillReturnResult(sqlmock.NewResult(0, 1))
	runner := &Runner{db: db}
	if err := runner.persistRunCompletion(context.Background(), "run-1", "summary", &RunState{}, `{"run_id":"run-1"}`); err != nil {
		t.Fatalf("transient failure not recovered: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionWriteDoesNotSignalSuccessWithoutUpdatedRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 3; i++ {
		mock.ExpectExec("UPDATE agent_runs").WillReturnResult(sqlmock.NewResult(0, 0))
	}
	runner := &Runner{db: db}
	err = runner.persistRunCompletion(context.Background(), "run-1", "summary", &RunState{}, `{"run_id":"run-1"}`)
	if err == nil || !strings.Contains(err.Error(), "persist agent run completion") {
		t.Fatalf("missing completion row should fail: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionWriteDoesNotRetryBeyondBound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 3; i++ {
		mock.ExpectExec("UPDATE agent_runs").WillReturnError(errors.New("database offline"))
	}
	runner := &Runner{db: db}
	if err := runner.persistRunCompletion(context.Background(), "run-1", "summary", &RunState{}, `{"run_id":"run-1"}`); err == nil {
		t.Fatal("permanent database failure ignored")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCompletionWritePreservesCompletedStatus(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("UPDATE agent_runs").WithArgs(models.AgentRunStatusCompleted, "summary", float64(4), sqlmock.AnyArg(), `{"run_id":"run-1"}`, "run-1").WillReturnResult(sqlmock.NewResult(0, 1))
	runner := &Runner{db: db}
	if err := runner.persistRunCompletion(context.Background(), "run-1", "summary", &RunState{CostSoFar: 4}, `{"run_id":"run-1"}`); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPendingCompletionSurvivesPublishFailure(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO agent_runs (id, task_id) VALUES ('run-1', 'task-1')`); err != nil {
		t.Fatal(err)
	}

	const payload = `{"run_id":"run-1","summary":"original"}`
	runner := &Runner{db: db}
	if err := runner.persistRunCompletion(ctx, "run-1", "original", &RunState{}, payload); err != nil {
		t.Fatal(err)
	}

	unavailable := errors.New("broker unavailable")
	if err := runner.publishPendingRunCompletion(ctx, "run-1", func([]byte) error { return unavailable }); !errors.Is(err, unavailable) {
		t.Fatalf("publish failure = %v, want broker error", err)
	}

	// Recreate the runner to verify the retry uses the durable payload.
	runner = &Runner{db: db}
	published := 0
	publish := func(data []byte) error {
		published++
		if string(data) != payload {
			t.Fatalf("published %s, want %s", data, payload)
		}
		return nil
	}
	for range 2 {
		if err := runner.publishPendingRunCompletion(ctx, "run-1", publish); err != nil {
			t.Fatal(err)
		}
	}
	if published != 1 {
		t.Fatalf("published %d completions, want one", published)
	}
}
