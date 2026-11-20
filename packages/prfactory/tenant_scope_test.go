package prfactory

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/models"
)

func TestGetRepositoryForProjectCannotCrossTenantBoundary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT full_name FROM repositories").WithArgs("repo-A", "project-A").
		WillReturnError(sql.ErrNoRows)
	f := NewFactory(db, nil)
	if _, _, err := f.getRepoOwnerNameForProject(context.Background(), "repo-A", "project-A"); err == nil {
		t.Fatal("repository missing from authorized project must be rejected")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestValidateFactoryWorkspaceTenantScope(t *testing.T) {
	task := &models.Task{ID: "task-A", ProjectID: "project-A", RepositoryID: "repo-A"}
	wsid := "workspace-A"
	run := &models.AgentRun{ID: "run-A", TaskID: "task-A", WorkspaceID: &wsid}
	ws := &models.Workspace{ID: "workspace-A", RepositoryID: "repo-A"}
	if err := validateFactoryWorkspace(task, run, ws); err != nil {
		t.Fatal(err)
	}
	ws.RepositoryID = "repo-B"
	if err := validateFactoryWorkspace(task, run, ws); err == nil {
		t.Fatal("workspace repository crossing rejected")
	}
	ws.RepositoryID = "repo-A"
	run.TaskID = "task-B"
	if err := validateFactoryWorkspace(task, run, ws); err == nil {
		t.Fatal("run task crossing rejected")
	}
	run.TaskID = "task-A"
	ws.TaskID = new(string)
	*ws.TaskID = "task-B"
	if err := validateFactoryWorkspace(task, run, ws); err == nil {
		t.Fatal("workspace task crossing rejected")
	}
	if err := validateFactoryWorkspace(task, run, nil); err == nil {
		t.Fatal("missing workspace rejected")
	}
}
