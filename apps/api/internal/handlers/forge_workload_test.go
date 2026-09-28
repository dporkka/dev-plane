package handlers

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/workloadauth"
)

const forgeWorkloadSecret = "0123456789abcdef0123456789abcdef"

func forgeHandler(t *testing.T) (*Handler, sqlmock.Sqlmock, *workloadauth.Verifier, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workloadauth.NewVerifier("nulang-cloud", forgeWorkloadSecret, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	verifier.Now = func() time.Time { return now }
	h := NewHandler(db, slog.Default()).
		WithCapabilityKernel(capability.NewKernel(nil, nil, nil, slog.Default())).
		WithWorkloadVerifier(verifier)
	return h, mock, verifier, func() { _ = db.Close() }
}

func forgeContextRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"task_id", "project_id", "repository_id", "task_workspace_id", "risk_level", "target_branch",
		"run_id", "run_workspace_id", "agent_role", "run_status", "prompt_tokens", "completion_tokens", "total_cost", "started_at", "completed_at",
		"repo_id", "repo_project_id", "owner", "name", "full_name", "default_branch",
		"organization_id", "workspace_id", "workspace_branch", "workspace_runtime_provider",
	}).AddRow(
		"task-1", "project-1", "repo-1", "workspace-1", "low", "main",
		"run-1", "workspace-1", "implementer", "running", 100, 50, 0.01, nil, nil,
		"repo-1", "project-1", "nulang-org", "nulang", "nulang-org/nulang", "main",
		"org-1", "workspace-1", "agent/task-1", "docker",
	)
}

func signedForgeRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/authorize", bytes.NewBufferString(body))
	workloadauth.Sign(req, "nulang-cloud", forgeWorkloadSecret, now, []byte(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestAuthorizeForgeWorkloadAllowsRepositoryRead(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows())

	req := signedForgeRequest(t, `{"task_id":"task-1","run_id":"run-1","operation":"repo.read"}`)
	rec := httptest.NewRecorder()
	h.AuthorizeForgeWorkload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response ForgeAuthorizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Allowed || response.RequiredApproval {
		t.Fatalf("unexpected decision: %+v", response)
	}
	if response.Repository.FullName != "nulang-org/nulang" {
		t.Fatalf("repository = %+v", response.Repository)
	}
	if response.Effect != "allow" {
		t.Fatalf("effect = %q", response.Effect)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizeForgeWorkloadKeepsMergeAdminOnly(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-1", "task-1").
		WillReturnRows(forgeContextRows())

	req := signedForgeRequest(t, `{"task_id":"task-1","run_id":"run-1","operation":"change.merge"}`)
	rec := httptest.NewRecorder()
	h.AuthorizeForgeWorkload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response ForgeAuthorizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Allowed || !response.RequiredApproval || response.Effect != "admin_only" {
		t.Fatalf("unexpected decision: %+v", response)
	}
}

func TestAuthorizeForgeWorkloadRejectsRunTaskMismatch(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()

	mock.ExpectQuery("(?s)SELECT.*FROM agent_runs ar.*JOIN tasks t.*JOIN repositories r.*JOIN projects p.*LEFT JOIN workspaces w").
		WithArgs("run-other", "task-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"task_id", "project_id", "repository_id", "task_workspace_id", "risk_level", "target_branch",
			"run_id", "run_workspace_id", "agent_role", "run_status", "prompt_tokens", "completion_tokens", "total_cost", "started_at", "completed_at",
			"repo_id", "repo_project_id", "owner", "name", "full_name", "default_branch",
			"organization_id", "workspace_id", "workspace_branch", "workspace_runtime_provider",
		}))

	req := signedForgeRequest(t, `{"task_id":"task-1","run_id":"run-other","operation":"repo.read"}`)
	rec := httptest.NewRecorder()
	h.AuthorizeForgeWorkload(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAuthorizeForgeWorkloadRejectsInvalidSignatureBeforeDatabase(t *testing.T) {
	h, mock, _, cleanup := forgeHandler(t)
	defer cleanup()

	body := `{"task_id":"task-1","run_id":"run-1","operation":"repo.read"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/forge/authorize", bytes.NewBufferString(body))
	workloadauth.Sign(req, "nulang-cloud", forgeWorkloadSecret, time.Unix(1_800_000_000, 0), []byte("{}"))
	rec := httptest.NewRecorder()
	h.AuthorizeForgeWorkload(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
