package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/gateway"
)

func TestLegacyMergeRejectsGiteaBeforeCallingGitHub(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	provider := &fakeMergeGateway{result: &gateway.MergePRResult{Merged: true}}
	h.WithGitHubToken("synthetic-token").WithGitHubGateway(provider)
	now := time.Now().UTC()
	expectAuthorizePullRequest(mock, "pr-1")
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").WithArgs("pr-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body", "branch", "base_branch", "url", "state", "draft", "created_by", "merged_at", "created_at", "updated_at", "owner", "name", "status", "settings",
		}).AddRow("pr-1", "task-1", nil, "repo-1", 42, "title", "body", "feature", "main", "https://forge.example/owner/repo/pulls/42", "open", false, testUserID, nil, now, now, "owner", "repo", "pr_created", `{"forge":{"provider":"gitea","base_url":"https://forge.example"}}`))
	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest("pr-1", `{"sha":"abc"}`))
	if rec.Code != http.StatusConflict || len(provider.calls) != 0 {
		t.Fatalf("Gitea merge did not fail closed: status=%d, calls=%d", rec.Code, len(provider.calls))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type recordingLegacyDeploy struct{ calls int }

func (d *recordingLegacyDeploy) CreateDeployment(context.Context, *oauth2.Token, string, string, string, string) (*gateway.Deployment, error) {
	d.calls++
	return &gateway.Deployment{ID: 42}, nil
}

func TestLegacyDeploymentRejectsGiteaBeforeCallingGitHub(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	provider := &recordingLegacyDeploy{}
	h.WithDeployToken("synthetic-token").WithDeployGateway(provider)
	expectAuthorizeTask(mock, "task-1")
	mock.ExpectQuery("SELECT status, repository_id, target_branch, project_id").WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "repository_id", "target_branch", "project_id"}).AddRow("pr_created", "repo-1", "main", "proj-1"))
	mock.ExpectQuery("SELECT owner, name, settings FROM repositories").WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"owner", "name", "settings"}).AddRow("owner", "repo", `{"forge":{"provider":"gitea","base_url":"https://forge.example"}}`))
	req := httptest.NewRequest(http.MethodPost, "/tasks/task-1/deploy", strings.NewReader(`{"environment":"staging"}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: testUserID, OrgID: testOrgID, Role: "admin"}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "task-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.DeployTask(rec, req)
	if rec.Code != http.StatusConflict || provider.calls != 0 {
		t.Fatalf("Gitea deployment did not fail closed: status=%d, calls=%d", rec.Code, provider.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAPICreatePRRejectsInvalidRuntimeConfiguration(t *testing.T) {
	t.Setenv("FORGE_PROVIDER", "unknown")
	t.Setenv("GITHUB_TOKEN", "must-not-fallback")
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectAuthorizeTask(mock, "task-1")
	mock.ExpectQuery("SELECT status, repository_id, target_branch").WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{"status", "repository_id", "target_branch"}).AddRow("reviewing", "repo-1", "main"))
	req := httptest.NewRequest(http.MethodPost, "/tasks/task-1/pull-request", strings.NewReader(`{"approved":true}`))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: testUserID, OrgID: testOrgID, Role: "admin"}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("taskId", "task-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.CreatePullRequest(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", rec.Code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
