package prfactory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreatePullRequestRejectsGiteaHostWorktreeAfterVerifiedBinding(t *testing.T) {
	var gets, mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
			http.Error(w, "mutation", http.StatusForbidden)
			return
		}
		gets.Add(1)
		root := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 77, "name": "widget", "full_name": "acme/widget", "html_url": root + "/acme/widget", "clone_url": root + "/acme/widget.git", "default_branch": "main"})
	}))
	defer server.Close()
	t.Setenv("FORGE_PROVIDER", "gitea")
	t.Setenv("GITEA_URL", server.URL)
	t.Setenv("GITEA_BINDING_GENERATION", "1")
	t.Setenv("GITEA_TOKEN", "synthetic-api-token")
	t.Setenv("GITEA_GIT_USERNAME", "writer")
	t.Setenv("GITEA_GIT_PASSWORD", "synthetic-push-token")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id").WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "repository_id", "workspace_id", "created_by", "source", "source_id", "title", "description", "status", "priority", "risk_level", "target_branch", "spec", "acceptance_criteria", "max_cost", "max_runtime_minutes", "approval_requirements", "metadata", "started_at", "completed_at", "created_at", "updated_at"}).
			AddRow("task-1", "project-1", "repo-1", "workspace-1", "user-1", "manual", nil, "fixture", nil, "reviewing", "medium", "low", "main", nil, nil, nil, 30, nil, nil, nil, nil, now, now))
	mock.ExpectQuery("SELECT id, task_id, workspace_id, agent_role").WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "workspace_id", "agent_role", "model", "provider", "status", "prompt_tokens", "completion_tokens", "total_cost", "error_message", "summary", "metadata", "created_at", "updated_at"}).
			AddRow("run-1", "task-1", "workspace-1", "coder", nil, nil, "completed", 0, 0, 0.0, nil, nil, []byte(`{}`), now, now))
	mock.ExpectQuery("SELECT id, run_id, summary, findings").WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "run_id", "summary", "findings", "risk_level", "approvable", "suggestions", "test_coverage", "security_notes", "diff_summary", "created_at"}).
			AddRow("review-1", "run-1", "fixture", nil, "low", true, nil, "fixture", "fixture", nil, now))
	mock.ExpectQuery("SELECT id, repository_id, task_id, name, branch").WithArgs("workspace-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "repository_id", "task_id", "name", "branch", "base_branch", "worktree_path", "runtime_provider", "runtime_session_id", "status", "preview_url", "settings", "created_at", "updated_at", "deleted_at"}).
			AddRow("workspace-1", "repo-1", "task-1", "fixture", "agent/test", "main", t.TempDir(), "local", nil, "ready", nil, nil, now, now, nil))
	settings := fmt.Sprintf(`{"forge":{"provider":"gitea","base_url":%q}}`, server.URL)
	mock.ExpectQuery("SELECT owner, name, settings FROM repositories").WithArgs("repo-1", "project-1").
		WillReturnRows(sqlmock.NewRows([]string{"owner", "name", "settings"}).AddRow("acme", "widget", settings))
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT r.owner,r.name,r.full_name,r.clone_url,r.settings,p.organization_id").WithArgs("repo-1", "project-1").
			WillReturnRows(sqlmock.NewRows([]string{"owner", "name", "full_name", "clone_url", "settings", "org", "bproject", "borg", "instance", "upstream", "bname", "generation", "revoked_at"}).
				AddRow("acme", "widget", "acme/widget", server.URL+"/acme/widget.git", settings, "org-1", "project-1", "org-1", server.URL, 77, "acme/widget", 1, nil))
	}
	factory := NewFactory(db, nil)
	pr, err := factory.CreatePullRequest(context.Background(), "task-1")
	if pr != nil || !errors.Is(err, ErrGiteaRepositoryBinding) || !strings.Contains(err.Error(), "host-worktree push") {
		t.Fatalf("worktree push not blocked: %v", err)
	}
	if gets.Load() != 1 || mutations.Load() != 0 {
		t.Fatalf("unexpected provider operations: get=%d mutations=%d", gets.Load(), mutations.Load())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
