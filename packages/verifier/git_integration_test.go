package verifier

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
	"github.com/ai-dev-control-plane/runtimes"
)

// realGitRuntime verifies the actual Git porcelain behavior against a disposable
// local checkout. Unlike fakeRuntime, this executes every requested git command.
type realGitRuntime struct {
	root string
}

func (r *realGitRuntime) ReadFile(_ context.Context, _ string, path string) ([]byte, error) {
	if path != "devplane.yaml" {
		return nil, fmt.Errorf("unexpected file request %q", path)
	}
	return os.ReadFile(filepath.Join(r.root, path))
}

func (r *realGitRuntime) ExecuteCommand(ctx context.Context, _ string, command runtimes.Command) (*runtimes.CommandResult, error) {
	var cmd *exec.Cmd
	switch {
	case len(command.Args) > 0:
		cmd = exec.CommandContext(ctx, command.Args[0], command.Args[1:]...)
	case command.Command != "":
		cmd = exec.CommandContext(ctx, "sh", "-c", command.Command)
	default:
		return nil, errors.New("missing executable")
	}
	cmd.Dir = r.root
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return nil, err
		}
		exitCode = exitErr.ExitCode()
	}
	return &runtimes.CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode}, nil
}

func disposableGitVerifierWorkspace(t *testing.T, gate string) (*realGitRuntime, string) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "--initial-branch=main")
	git("config", "user.name", "Verifier Test")
	git("config", "user.email", "verifier@example.invalid")
	git("config", "commit.gpgsign", "false")
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "feature.go"), []byte("package feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("version: 1\nverification:\n  changed:\n    command: %s\nwork:\n  isolation: worktree\n  max_parallel_cost: 1\n", gate)
	if err := os.WriteFile(filepath.Join(root, "devplane.yaml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "baseline")
	return &realGitRuntime{root: root}, git("rev-parse", "HEAD")
}

func TestVerifierUsesRealGitToRejectUncommittedWorkspaceChanges(t *testing.T) {
	for _, tc := range []struct {
		name      string
		gate      string
		modify    func(*testing.T, string)
		wantError string
	}{
		{name: "clean checkout", gate: "git rev-parse HEAD"},
		{
			name: "tracked modification",
			gate: "git rev-parse HEAD",
			modify: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "src", "feature.go"), []byte("package changed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "uncommitted changes",
		},
		{
			name: "untracked file",
			gate: "git rev-parse HEAD",
			modify: func(t *testing.T, root string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("untracked\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantError: "uncommitted changes",
		},
		{
			name:      "passing gate writes an untracked artifact",
			gate:      "printf generated > generated.txt",
			wantError: "uncommitted changes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, head := disposableGitVerifierWorkspace(t, tc.gate)
			if tc.modify != nil {
				tc.modify(t, runtime.root)
			}
			store := &fakeStore{}
			work := repoprotocol.WorkItem{
				ID:             "task-1",
				Repository:     "test/repository",
				Objective:      "Verify committed source",
				OwnershipPaths: []string{"src/feature.go"},
				Risk:           repoprotocol.RiskLow,
				Cost:           1,
				RequiredGates:  []string{"changed"},
				BaseSHA:        head,
				State:          repoprotocol.WorkVerifying,
			}
			result, err := New(runtime, store).Verify(context.Background(), Request{
				SessionID:     "disposable",
				WorkItem:      work,
				CandidateHead: head,
				ChangedPaths:  []string{"src/feature.go"},
			})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("Verify() error = %v, want %q", err, tc.wantError)
				}
				if len(store.bundles) != 0 || len(store.workItems) != 0 {
					t.Fatalf("failed verification advanced work: evidence=%d items=%d", len(store.bundles), len(store.workItems))
				}
				return
			}
			if err != nil {
				t.Fatalf("Verify() clean checkout failed: %v", err)
			}
			if len(store.bundles) != 1 || len(store.workItems) != 1 {
				t.Fatalf("clean verification not recorded: evidence=%d items=%d", len(store.bundles), len(store.workItems))
			}
			if err := result.Evidence.ValidateHead(head); err != nil {
				t.Fatalf("clean evidence not bound to candidate: %v", err)
			}
		})
	}
}
