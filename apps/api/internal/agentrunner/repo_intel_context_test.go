package agentrunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/projectbrain"
)

type fixedContextSource struct {
	name  string
	facts []projectbrain.Fact
}

func (s fixedContextSource) Name() string { return s.name }
func (s fixedContextSource) Facts(context.Context, projectbrain.Query) ([]projectbrain.Fact, error) {
	return append([]projectbrain.Fact(nil), s.facts...), nil
}

type recordingWorkspaceSourceFactory struct {
	path   string
	source projectbrain.Source
}

func (f *recordingWorkspaceSourceFactory) SourceForWorkspace(path string) projectbrain.Source {
	f.path = path
	return f.source
}

func TestWorkspaceContextCompilerCombinesStaticAndWorkspaceSources(t *testing.T) {
	static := fixedContextSource{name: "static", facts: []projectbrain.Fact{{
		Subject: "symbol:static", Relation: "defined_in", Object: "static.go:1",
		Repository: "repo-1", Revision: "git-commit:abc123", Scope: projectbrain.ScopeRevision,
		Provenance: []projectbrain.Provenance{{Source: "static", Confidence: 1}},
	}}}
	workspace := fixedContextSource{name: "workspace", facts: []projectbrain.Fact{{
		Subject: "symbol:workspace", Relation: "defined_in", Object: "workspace.go:2",
		Repository: "repo-1", Revision: "git-commit:abc123", Scope: projectbrain.ScopeRevision,
		Provenance: []projectbrain.Provenance{{Source: "workspace", Confidence: 1}},
	}}}
	factory := &recordingWorkspaceSourceFactory{source: workspace}
	compiler := NewWorkspaceContextCompiler(static).WithSourceFactory(factory)

	pkg, err := compiler.CompileWorkspace(context.Background(), projectbrain.CompileRequest{
		Repository: "repo-1", Revision: "git-commit:abc123", Objective: "fix workspace",
	}, "/tmp/workspace-1")
	if err != nil {
		t.Fatalf("CompileWorkspace() error = %v", err)
	}
	if factory.path != "/tmp/workspace-1" {
		t.Fatalf("workspace path = %q", factory.path)
	}
	if len(pkg.Facts) != 2 {
		t.Fatalf("facts = %#v", pkg.Facts)
	}
	if strings.Join(pkg.Sources, "|") != "static|workspace" {
		t.Fatalf("sources = %#v", pkg.Sources)
	}
}

func TestRepoIntelContextSourceEmitsRevisionBoundSymbols(t *testing.T) {
	repo, head := initRepoIntelTestRepo(t)
	source := NewRepoIntelContextSource(repo)
	facts, err := source.Facts(context.Background(), projectbrain.Query{
		Repository: "repo-1",
		Revision:   "git-commit:" + head,
		Terms:      []string{"saveInvoice"},
		ChangedPaths: []string{"invoice.go"},
	})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	if len(facts) == 0 {
		t.Fatal("Facts() returned no facts")
	}
	found := false
	for _, fact := range facts {
		if fact.Subject == "symbol:saveInvoice" && fact.Relation == "defined_in" && strings.HasPrefix(fact.Object, "invoice.go:") {
			found = true
		}
		if fact.Repository != "repo-1" || fact.Revision != "git-commit:"+head || fact.Scope != projectbrain.ScopeRevision {
			t.Fatalf("fact not revision-bound: %#v", fact)
		}
		if len(fact.Provenance) != 1 || fact.Provenance[0].Source != "repo-intel" {
			t.Fatalf("fact provenance = %#v", fact.Provenance)
		}
	}
	if !found {
		t.Fatalf("saveInvoice definition missing: %#v", facts)
	}
}

func TestRepoIntelContextSourceRejectsDirtyWorkspace(t *testing.T) {
	repo, head := initRepoIntelTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "invoice.go"), []byte("package invoice\nfunc saveInvoice() {}\nfunc dirty() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewRepoIntelContextSource(repo).Facts(context.Background(), projectbrain.Query{
		Repository: "repo-1", Revision: "git-commit:" + head, Terms: []string{"saveInvoice"},
	})
	if err == nil || !strings.Contains(err.Error(), "working tree") {
		t.Fatalf("Facts() error = %v, want working tree mismatch", err)
	}
}

func TestRepoIntelContextSourceRejectsDifferentCommit(t *testing.T) {
	repo, _ := initRepoIntelTestRepo(t)
	_, err := NewRepoIntelContextSource(repo).Facts(context.Background(), projectbrain.Query{
		Repository: "repo-1", Revision: "git-commit:0000000000000000000000000000000000000000",
	})
	if err == nil || !strings.Contains(err.Error(), "HEAD") {
		t.Fatalf("Facts() error = %v, want HEAD mismatch", err)
	}
}

func initRepoIntelTestRepo(t *testing.T) (string, string) {
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
