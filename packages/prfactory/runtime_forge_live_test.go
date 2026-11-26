//go:build gitealive

package prfactory

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/gateway"
)

// Run only through testdata/live_gitea.py, which provisions a fresh loopback
// forge and supplies synthetic fixture credentials.
func TestLiveGiteaRuntimePublication(t *testing.T) {
	base, token, expected := os.Getenv("DEV_PLANE_LIVE_GITEA_BASE"), os.Getenv("DEV_PLANE_LIVE_GITEA_TOKEN"), os.Getenv("DEV_PLANE_LIVE_GITEA_HEAD")
	root, err := url.Parse(base)
	if err != nil || root.Scheme != "http" || root.Hostname() != "127.0.0.1" || root.Port() == "" || root.User != nil || root.Path != "" || root.RawQuery != "" || root.Fragment != "" || token == "" || len(expected) != 40 {
		t.Fatal("live qualification requires the disposable loopback fixture")
	}
	t.Setenv("FORGE_PROVIDER", "gitea")
	t.Setenv("GITEA_URL", base)
	t.Setenv("GITEA_BINDING_GENERATION", "1")
	t.Setenv("GITEA_TOKEN", token)
	t.Setenv("GITEA_GIT_USERNAME", "")
	t.Setenv("GITEA_GIT_PASSWORD", "")
	t.Setenv("GITHUB_TOKEN", "synthetic-github-token-must-not-be-used")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, project_id, repository_id, workspace_id").WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "repository_id", "workspace_id", "created_by", "source", "source_id", "title", "description", "status", "priority", "risk_level", "target_branch", "spec", "acceptance_criteria", "max_cost", "max_runtime_minutes", "approval_requirements", "metadata", "started_at", "completed_at", "created_at", "updated_at"}).AddRow("task-1", "project-1", "repo-1", "workspace-1", "user-1", "manual", nil, "Synthetic agent change", nil, "reviewing", "medium", "low", "main", nil, nil, nil, 30, nil, nil, nil, nil, now, now))
	mock.ExpectQuery("SELECT id, task_id, workspace_id, agent_role").WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "task_id", "workspace_id", "agent_role", "model", "provider", "status", "prompt_tokens", "completion_tokens", "total_cost", "error_message", "summary", "metadata", "created_at", "updated_at"}).AddRow("run-1", "task-1", "workspace-1", "coder", nil, nil, "completed", 0, 0, 0.0, nil, nil, []byte(`{}`), now, now))
	mock.ExpectQuery("SELECT id, run_id, summary, findings").WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "run_id", "summary", "findings", "risk_level", "approvable", "suggestions", "test_coverage", "security_notes", "diff_summary", "created_at"}).AddRow("review-1", "run-1", "synthetic review", nil, "low", true, nil, "fixture", "fixture", nil, now))
	mock.ExpectQuery("SELECT id, repository_id, task_id, name, branch").WithArgs("workspace-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "repository_id", "task_id", "name", "branch", "base_branch", "worktree_path", "runtime_provider", "runtime_session_id", "status", "preview_url", "settings", "created_at", "updated_at", "deleted_at"}).AddRow("workspace-1", "repo-1", "task-1", "pilot", "agent-pilot", "main", nil, "local", nil, "ready", nil, nil, now, now, nil))
	mock.ExpectQuery("SELECT owner, name, settings FROM repositories").WithArgs("repo-1", "project-1").
		WillReturnRows(sqlmock.NewRows([]string{"owner", "name", "settings"}).AddRow("forgepilot", "pilot", fmt.Sprintf(`{"forge":{"provider":"gitea","base_url":%q}}`, base)))
	f := NewFactory(db, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	if f.configErr != nil {
		t.Fatal(f.configErr)
	}
	repos, err := f.forge.ListRepositories(ctx, f.forgeCredential, 1)
	if err != nil || len(repos) != 1 || repos[0].FullName != "forgepilot/pilot" || !repos[0].Private {
		t.Fatal("private fixture repo discovery failed")
	}
	repo, err := f.forge.GetRepository(ctx, f.forgeCredential, "forgepilot", "pilot")
	if err != nil || !repo.Private || repo.DefaultBranch != "main" {
		t.Fatal("private fixture repository lookup failed")
	}
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT r.owner,r.name,r.full_name,r.clone_url,r.settings,p.organization_id").WithArgs("repo-1", "project-1").
			WillReturnRows(sqlmock.NewRows([]string{"owner", "name", "full_name", "clone_url", "settings", "org", "bproject", "borg", "instance", "upstream", "bname", "generation", "revoked_at"}).
				AddRow("forgepilot", "pilot", "forgepilot/pilot", repo.CloneURL, fmt.Sprintf(`{"forge":{"provider":"gitea","base_url":%q}}`, base), "org-1", "project-1", "org-1", base, repo.ID, "forgepilot/pilot", 1, nil))
	}
	mock.ExpectExec("INSERT INTO pull_requests").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE tasks SET status = 'pr_created'").WillReturnResult(sqlmock.NewResult(0, 1))
	pr, err := f.CreatePullRequest(ctx, "task-1")
	if err != nil {
		t.Fatalf("runtime publication failed: %v", err)
	}
	if pr.Number <= 0 || pr.Branch != "agent-pilot" || pr.BaseBranch != "main" {
		t.Fatal("unexpected published PR identity")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	prURL := base + "/api/v1/repos/forgepilot/pilot/pulls/" + strconv.Itoa(pr.Number)
	readPR := func() (bool, string, string) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, prURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "token "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal("independent PR state lookup failed")
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PR lookup returned %d", resp.StatusCode)
		}
		var state struct {
			Merged      bool   `json:"merged"`
			MergeCommit string `json:"merge_commit_sha"`
			Head        struct {
				SHA string `json:"sha"`
			} `json:"head"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&state); err != nil {
			t.Fatal("invalid PR state")
		}
		return state.Merged, state.Head.SHA, state.MergeCommit
	}
	merged, head, _ := readPR()
	if merged || head != expected {
		t.Fatal("PR head was not independently confirmed")
	}
	wrong := "0" + expected[1:]
	if wrong == expected {
		wrong = "1" + expected[1:]
	}
	merge := func(sha string) (*gateway.ForgeMergeResult, error) {
		return f.forge.MergePullRequest(ctx, f.forgeCredential, "forgepilot", "pilot", pr.Number, gateway.ForgeMergeRequest{Method: "merge", ExpectedHeadSHA: sha})
	}
	// This retry is only a disposable test fixture workaround, not production
	// mutation retry policy. Require the provider to prove stale-head rejection.
	staleRejected := false
	for i := 0; i < 40; i++ {
		_, err = merge(wrong)
		if err == nil {
			t.Fatal("stale head unexpectedly merged")
		}
		if strings.Contains(err.Error(), "gitea API error 409") {
			staleRejected = true
			break
		}
		if !strings.Contains(err.Error(), "gitea API error 405") {
			t.Fatalf("unexpected stale-head refusal: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !staleRejected {
		t.Fatal("Gitea never returned explicit stale-head refusal")
	}
	merged, head, _ = readPR()
	if merged || head != expected {
		t.Fatal("stale refusal changed PR state")
	}
	accepted := false
	for i := 0; i < 40; i++ {
		result, err := merge(expected)
		if err == nil && result != nil && result.Merged {
			accepted = true
			break
		}
		if err == nil || !strings.Contains(err.Error(), "gitea API error 405") {
			t.Fatalf("unexpected exact-head merge refusal: %v", err)
		}
		merged, head, _ = readPR()
		if merged || head != expected {
			t.Fatal("PR changed after temporary merge refusal")
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !accepted {
		t.Fatal("Gitea never accepted expected head")
	}
	merged, _, commit := readPR()
	if !merged || commit == "" {
		t.Fatal("merge was not independently confirmed")
	}
	t.Log("runtime Gitea PR publication, local records, stale-head rejection, exact-head merge verified")
}
