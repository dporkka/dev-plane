package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/models"
)

func newChangeGraphRequest(method, path, prID, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{
		UserID: testUserID, OrgID: testOrgID, Email: "test@example.com", Role: models.RoleOwner,
	}))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", prID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

func TestAddPullRequestDependencyCreatesAcyclicEdge(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizePullRequest(mock, "pr-web")
	expectAuthorizePullRequest(mock, "pr-api")
	mock.ExpectQuery("SELECT c.id, t.project_id").
		WithArgs("pr-web").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id"}).AddRow("candidate-web", "project-1"))
	mock.ExpectQuery("SELECT c.id, t.project_id").
		WithArgs("pr-api").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id"}).AddRow("candidate-api", "project-1"))
	mock.ExpectQuery("SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash").
		WithArgs("project-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pull_request_id", "repository_id", "commit_sha", "tree_hash", "state", "depends_on_candidate_id",
		}).
			AddRow("candidate-api", "pr-api", "repo-api", "sha-api", "tree-api", "open", nil).
			AddRow("candidate-web", "pr-web", "repo-web", "sha-web", "tree-web", "open", nil))
	mock.ExpectExec("INSERT INTO candidate_dependencies").
		WithArgs("candidate-web", "candidate-api", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.AddPullRequestDependency(rec, newChangeGraphRequest(
		http.MethodPost,
		"/pull-requests/pr-web/dependencies",
		"pr-web",
		"{\"depends_on_pull_request_id\":\"pr-api\"}",
	))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestAddPullRequestDependencyRejectsCycle(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizePullRequest(mock, "pr-api")
	expectAuthorizePullRequest(mock, "pr-web")
	mock.ExpectQuery("SELECT c.id, t.project_id").
		WithArgs("pr-api").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id"}).AddRow("candidate-api", "project-1"))
	mock.ExpectQuery("SELECT c.id, t.project_id").
		WithArgs("pr-web").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id"}).AddRow("candidate-web", "project-1"))
	mock.ExpectQuery("SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash").
		WithArgs("project-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pull_request_id", "repository_id", "commit_sha", "tree_hash", "state", "depends_on_candidate_id",
		}).
			AddRow("candidate-api", "pr-api", "repo-api", "sha-api", "tree-api", "open", nil).
			AddRow("candidate-web", "pr-web", "repo-web", "sha-web", "tree-web", "open", "candidate-api"))

	rec := httptest.NewRecorder()
	h.AddPullRequestDependency(rec, newChangeGraphRequest(
		http.MethodPost,
		"/pull-requests/pr-api/dependencies",
		"pr-api",
		"{\"depends_on_pull_request_id\":\"pr-web\"}",
	))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestGetPullRequestChangeGraphReportsTransitiveBlockers(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizePullRequest(mock, "pr-web")
	mock.ExpectQuery("SELECT c.id, t.project_id").
		WithArgs("pr-web").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id"}).AddRow("candidate-web", "project-1"))
	mock.ExpectQuery("SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash").
		WithArgs("project-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pull_request_id", "repository_id", "commit_sha", "tree_hash", "state", "depends_on_candidate_id",
		}).
			AddRow("candidate-schema", "pr-schema", "repo-db", "sha-schema", "tree-schema", "merged", nil).
			AddRow("candidate-api", "pr-api", "repo-api", "sha-api", "tree-api", "open", "candidate-schema").
			AddRow("candidate-web", "pr-web", "repo-web", "sha-web", "tree-web", "open", "candidate-api"))

	rec := httptest.NewRecorder()
	h.GetPullRequestChangeGraph(rec, newChangeGraphRequest(
		http.MethodGet,
		"/pull-requests/pr-web/change-graph",
		"pr-web",
		"",
	))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"merge_ready\":false") ||
		!strings.Contains(rec.Body.String(), "\"candidate-api\"") {
		t.Fatalf("unexpected graph response: %s", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}
