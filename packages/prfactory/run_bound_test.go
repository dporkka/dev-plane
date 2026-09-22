package prfactory

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreatePullRequestForRunRejectsNonIntegratedCandidate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT status, integrated_commit").
		WithArgs("run-1", "task-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "integrated_commit"}).
			AddRow("verified", nil))

	factory := NewFactory(db, nil)
	_, err = factory.CreatePullRequestForRun(context.Background(), "task-1", "run-1")
	if err == nil || !strings.Contains(err.Error(), "expected integrated") {
		t.Fatalf("error = %v, want non-integrated rejection", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled expectations: %v", err)
	}
}

func TestCreatePullRequestForRunRejectsStaleApprovedRun(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT status, integrated_commit").
		WithArgs("run-old", "task-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "integrated_commit"}).
			AddRow("integrated", "commit-1"))

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, task_id, workspace_id, agent_role, model, provider, status").
		WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "workspace_id", "agent_role", "model", "provider", "status",
			"prompt_tokens", "completion_tokens", "total_cost", "error_message", "summary",
			"metadata", "created_at", "updated_at",
		}).AddRow(
			"run-new", "task-1", nil, "implementer", nil, nil, "completed",
			0, 0, 0.0, nil, nil, []byte("{}"), now, now,
		))

	factory := NewFactory(db, nil)
	_, err = factory.CreatePullRequestForRun(context.Background(), "task-1", "run-old")
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want stale run rejection", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled expectations: %v", err)
	}
}

func TestCreatePullRequestForRunRequiresCandidateRecord(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT status, integrated_commit").
		WithArgs("run-1", "task-1").
		WillReturnError(sql.ErrNoRows)

	factory := NewFactory(db, nil)
	_, err = factory.CreatePullRequestForRun(context.Background(), "task-1", "run-1")
	if err == nil || !strings.Contains(err.Error(), "no verified candidate") {
		t.Fatalf("error = %v, want missing candidate rejection", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled expectations: %v", err)
	}
}
