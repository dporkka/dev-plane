package agentexecutor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/api/internal/agentrunner"
	"github.com/ai-dev-control-plane/projectbrain"
)

type fakeExecutorGitNexusClient struct {
	commit string
}

func (f *fakeExecutorGitNexusClient) Snapshot(context.Context, string) (projectbrain.GitNexusSnapshot, error) {
	return projectbrain.GitNexusSnapshot{LastCommit: f.commit, IndexedAt: time.Unix(1, 0).UTC()}, nil
}

func (f *fakeExecutorGitNexusClient) Query(context.Context, string, []string, int) (projectbrain.GitNexusResult, error) {
	return projectbrain.GitNexusResult{Nodes: []projectbrain.GitNexusNode{{
		UID: "fn:saveInvoice", Name: "saveInvoice", FilePath: "invoice.go", StartLine: 2, Confidence: 1,
	}}}, nil
}

func TestNewProjectBrainProviderUsesFirstPartyRepoIntel(t *testing.T) {
	repo, head := initProjectBrainCompositionRepo(t)
	provider := newProjectBrainProvider(nil)
	pkg, err := provider.CompileWorkspace(context.Background(), projectbrain.CompileRequest{
		Repository: "repo-1", Revision: "git-commit:" + head, Objective: "fix saveInvoice",
		ChangedPaths: []string{"invoice.go"},
	}, repo)
	if err != nil {
		t.Fatalf("CompileWorkspace() error = %v", err)
	}
	if !containsString(pkg.Sources, "repo-intel") {
		t.Fatalf("sources = %#v, want repo-intel", pkg.Sources)
	}
	if containsString(pkg.Sources, "gitnexus") {
		t.Fatalf("sources = %#v, did not configure GitNexus", pkg.Sources)
	}
}

func TestNewProjectBrainProviderAddsExplicitGitNexusClient(t *testing.T) {
	repo, head := initProjectBrainCompositionRepo(t)
	provider := newProjectBrainProvider(&fakeExecutorGitNexusClient{commit: head})
	pkg, err := provider.CompileWorkspace(context.Background(), projectbrain.CompileRequest{
		Repository: "repo-1", Revision: "git-commit:" + head, Objective: "fix saveInvoice",
		ChangedPaths: []string{"invoice.go"},
	}, repo)
	if err != nil {
		t.Fatalf("CompileWorkspace() error = %v", err)
	}
	if !containsString(pkg.Sources, "repo-intel") || !containsString(pkg.Sources, "gitnexus") {
		t.Fatalf("sources = %#v, want independent repo-intel and gitnexus", pkg.Sources)
	}
}

func TestProjectBrainConfigurationRetainsGitNexusAcrossCallOrder(t *testing.T) {
	client := &fakeExecutorGitNexusClient{commit: "abc123"}
	tests := []struct {
		name      string
		configure func(*Executor) *Executor
	}{
		{
			name: "enable then gitnexus",
			configure: func(executor *Executor) *Executor {
				return executor.EnableProjectBrain().WithGitNexusClient(client)
			},
		},
		{
			name: "gitnexus then enable",
			configure: func(executor *Executor) *Executor {
				return executor.WithGitNexusClient(client).EnableProjectBrain()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor := &Executor{runner: &agentrunner.Runner{}}
			if got := tt.configure(executor); got != executor {
				t.Fatalf("configuration returned %p, want original executor %p", got, executor)
			}
			if !executor.projectBrainEnabled {
				t.Fatal("project brain configuration was not retained")
			}
			if executor.gitNexusClient != client {
				t.Fatalf("gitnexus client = %#v, want configured client", executor.gitNexusClient)
			}
		})
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func initProjectBrainCompositionRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Project Brain Test", "GIT_AUTHOR_EMAIL=project-brain@example.invalid",
			"GIT_COMMITTER_NAME=Project Brain Test", "GIT_COMMITTER_EMAIL=project-brain@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init")
	if err := os.WriteFile(filepath.Join(repo, "invoice.go"), []byte("package invoice\nfunc saveInvoice() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "invoice.go")
	run("commit", "-m", "base")
	return repo, run("rev-parse", "HEAD")
}
