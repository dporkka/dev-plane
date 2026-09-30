package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/forge"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

type fakeForgeMergeProvider struct {
	result     *forge.MergeResult
	err        error
	calls      []forge.MergeChangeRequest
	credential forge.Credential
	repository forge.Repository
	number     int
}

func (f *fakeForgeMergeProvider) Name() string { return "fake" }

func (f *fakeForgeMergeProvider) OpenChange(context.Context, forge.Credential, forge.Repository, forge.OpenChangeRequest) (*forge.Change, error) {
	return nil, errors.New("open change not used by merge handler")
}

func (f *fakeForgeMergeProvider) MergeChange(_ context.Context, credential forge.Credential, repository forge.Repository, number int, req forge.MergeChangeRequest) (*forge.MergeResult, error) {
	f.calls = append(f.calls, req)
	f.credential = credential
	f.repository = repository
	f.number = number
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

var _ forge.Provider = (*fakeForgeMergeProvider)(nil)

func newMergeRequest(prID string, body string) *http.Request {
	return newMergeRequestWithRole(prID, body, models.RoleOwner)
}

func newMergeRequestWithRole(prID, body, role string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/pull-requests/"+prID+"/merge", strings.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{
		UserID: testUserID,
		OrgID:  testOrgID,
		Email:  "test@example.com",
		Role:   role,
	}))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", prID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

func TestMergePullRequest(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	fakeForge := &fakeForgeMergeProvider{result: &forge.MergeResult{Merged: true, Revision: "abc123"}}
	h = h.WithForgeProvider(fakeForge).WithForgeCredential("forge-token")

	pub := &fakeEventPublisher{}
	h = h.WithEventPublisher(pub)

	prID := "pr-1"
	taskID := "task-1"
	repoID := "repo-1"
	now := time.Now().UTC()

	expectAuthorizePullRequest(mock, prID)
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs(prID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body",
			"branch", "base_branch", "url", "state", "draft", "created_by", "merged_at",
			"created_at", "updated_at", "owner", "name", "status",
		}).AddRow(
			prID, taskID, nil, repoID, 42, "title", "body",
			"feature", "main", "https://github.com/owner/repo/pull/42", "open", false, testUserID, nil,
			now, now, "owner", "repo", "pr_created",
		))
	mock.ExpectExec("UPDATE pull_requests SET state").
		WithArgs(sqlmock.AnyArg(), prID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE tasks SET status").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest(prID, `{"merge_method":"squash"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	if len(fakeForge.calls) != 1 {
		t.Fatalf("merge calls = %d, want 1", len(fakeForge.calls))
	}
	if fakeForge.calls[0].Method != "squash" {
		t.Errorf("merge method = %q, want squash", fakeForge.calls[0].Method)
	}

	if pub.subject != events.PRMerged {
		t.Errorf("published subject = %q, want %q", pub.subject, events.PRMerged)
	}
}

func TestMergePullRequest_AlreadyMerged(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	prID := "pr-1"
	taskID := "task-1"
	repoID := "repo-1"
	now := time.Now().UTC()

	expectAuthorizePullRequest(mock, prID)
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs(prID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body",
			"branch", "base_branch", "url", "state", "draft", "created_by", "merged_at",
			"created_at", "updated_at", "owner", "name", "status",
		}).AddRow(
			prID, taskID, nil, repoID, 42, "title", "body",
			"feature", "main", "https://github.com/owner/repo/pull/42", "merged", false, testUserID, &now,
			now, now, "owner", "repo", "pr_created",
		))

	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest(prID, ""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestMergePullRequest_WrongTaskStatus(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	prID := "pr-1"
	taskID := "task-1"
	repoID := "repo-1"
	now := time.Now().UTC()

	expectAuthorizePullRequest(mock, prID)
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs(prID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body",
			"branch", "base_branch", "url", "state", "draft", "created_by", "merged_at",
			"created_at", "updated_at", "owner", "name", "status",
		}).AddRow(
			prID, taskID, nil, repoID, 42, "title", "body",
			"feature", "main", "https://github.com/owner/repo/pull/42", "open", false, testUserID, nil,
			now, now, "owner", "repo", "running",
		))

	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest(prID, ""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestMergePullRequest_GitHubError(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	fakeForge := &fakeForgeMergeProvider{err: errors.New("merge conflict")}
	h = h.WithForgeProvider(fakeForge).WithForgeCredential("forge-token")

	prID := "pr-1"
	taskID := "task-1"
	repoID := "repo-1"
	now := time.Now().UTC()

	expectAuthorizePullRequest(mock, prID)
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs(prID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body",
			"branch", "base_branch", "url", "state", "draft", "created_by", "merged_at",
			"created_at", "updated_at", "owner", "name", "status",
		}).AddRow(
			prID, taskID, nil, repoID, 42, "title", "body",
			"feature", "main", "https://github.com/owner/repo/pull/42", "open", false, testUserID, nil,
			now, now, "owner", "repo", "pr_created",
		))

	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest(prID, ""))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestMergePullRequest_DeniedByPolicy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	denyAll := policies.NewEngine([]policies.Policy{
		{Name: "deny_all", ResourceType: "*", Action: "*", Effect: policies.EffectDeny},
	})
	h := NewHandler(db, slog.Default()).WithCapabilityKernel(capability.NewKernel(denyAll, nil, nil, slog.Default()))

	prID := "pr-1"
	taskID := "task-1"
	repoID := "repo-1"
	now := time.Now().UTC()

	expectAuthorizePullRequest(mock, prID)
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs(prID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body",
			"branch", "base_branch", "url", "state", "draft", "created_by", "merged_at",
			"created_at", "updated_at", "owner", "name", "status",
		}).AddRow(
			prID, taskID, nil, repoID, 42, "title", "body",
			"feature", "main", "https://github.com/owner/repo/pull/42", "open", false, testUserID, nil,
			now, now, "owner", "repo", "pr_created",
		))

	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest(prID, ""))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestMergePullRequest_MissingToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	prID := "pr-1"
	taskID := "task-1"
	repoID := "repo-1"
	now := time.Now().UTC()

	expectAuthorizePullRequest(mock, prID)
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs(prID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body",
			"branch", "base_branch", "url", "state", "draft", "created_by", "merged_at",
			"created_at", "updated_at", "owner", "name", "status",
		}).AddRow(
			prID, taskID, nil, repoID, 42, "title", "body",
			"feature", "main", "https://github.com/owner/repo/pull/42", "open", false, testUserID, nil,
			now, now, "owner", "repo", "pr_created",
		))

	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest(prID, ""))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestMergePullRequest_Unauthorized(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/pull-requests/pr-1/merge", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "pr-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.MergePullRequest(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMergePullRequest_NotFound(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	prID := "missing"
	expectAuthorizePullRequest(mock, prID)
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs(prID).
		WillReturnError(sql.ErrNoRows)

	rec := httptest.NewRecorder()
	h.MergePullRequest(rec, newMergeRequest(prID, ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
