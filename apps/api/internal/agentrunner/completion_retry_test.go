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
	if err := runner.persistRunCompletion(context.Background(), "run-1", "summary", &RunState{}); err != nil {
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
	err = runner.persistRunCompletion(context.Background(), "run-1", "summary", &RunState{})
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
	if err := runner.persistRunCompletion(context.Background(), "run-1", "summary", &RunState{}); err == nil {
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
	mock.ExpectExec("UPDATE agent_runs").WithArgs(models.AgentRunStatusCompleted, "summary", float64(4), sqlmock.AnyArg(), "run-1").WillReturnResult(sqlmock.NewResult(0, 1))
	runner := &Runner{db: db}
	if err := runner.persistRunCompletion(context.Background(), "run-1", "summary", &RunState{CostSoFar: 4}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
