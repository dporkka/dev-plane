package prfactory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

func TestGiteaIdentityPersistenceRollsBackOnReceiptFailure(t *testing.T) {
	t.Setenv("GITEA_BINDING_GENERATION", "1")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	forge, err := gateway.NewGiteaForge("https://forge.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	factory := &Factory{db: db, forge: forge}
	task := &models.Task{ID: "task", RepositoryID: "repo", ProjectID: "project"}
	run := &models.AgentRun{ID: "run"}
	now := time.Now().UTC()
	pr := &models.PullRequest{ID: "local-pr", TaskID: task.ID, RepoID: task.RepositoryID, RunID: &run.ID, Number: 7, Title: "reviewed", Branch: "agent/task", BaseBranch: "main", URL: "https://forge.example/acme/widget/pulls/7", CreatedBy: "user", CreatedAt: now, UpdatedAt: now}
	revision := giteaPRRevision{approvalID: "approval", headSHA: strings.Repeat("a", 40), baseSHA: strings.Repeat("b", 40), upstreamRepositoryID: 77}
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO pull_requests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO gitea_pr_identity_receipts").WillReturnError(errors.New("injected receipt write failure"))
	mock.ExpectRollback()
	err = factory.saveGiteaPRIdentity(context.Background(), task, run, repositoryTarget{Owner: "acme", Name: "widget"}, pr, 70, revision)
	if !errors.Is(err, ErrReviewedPush) {
		t.Fatalf("expected reconciliation error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
