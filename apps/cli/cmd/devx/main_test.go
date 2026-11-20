package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFindRepoRootFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, ok := findRepoRoot(nested)
	if !ok || got != root {
		t.Fatalf("findRepoRoot() = %q, %v; want %q, true", got, ok, root)
	}
}

func TestFindRepoRootSupportsWorktreeGitFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /tmp/example\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := findRepoRoot(root)
	if !ok || got != root {
		t.Fatalf("findRepoRoot() = %q, %v; want %q, true", got, ok, root)
	}
}

func TestResolveInvocationUsesRepoDelegate(t *testing.T) {
	root := t.TempDir()
	cfg := `{"version":1,"delegate":["node","scripts/devctl.mjs"]}`
	if err := os.WriteFile(filepath.Join(root, configName), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := resolveInvocation(root, true, []string{"verify", "ADACAVO-1", "changed"})
	if err != nil {
		t.Fatal(err)
	}
	want := invocation{Dir: root, Command: "node", Args: []string{"scripts/devctl.mjs", "verify", "ADACAVO-1", "changed"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveInvocation() = %#v; want %#v", got, want)
	}
}

func TestResolveInvocationFallsBackToDevPlane(t *testing.T) {
	cwd := t.TempDir()
	got, err := resolveInvocation(cwd, false, []string{"tasks", "get", "t1"})
	if err != nil {
		t.Fatal(err)
	}
	want := invocation{Dir: cwd, Command: "dev-plane", Args: []string{"tasks", "get", "t1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveInvocation() = %#v; want %#v", got, want)
	}
}

func TestResolveInvocationRejectsEmptyDelegate(t *testing.T) {
	root := t.TempDir()
	cfg := `{"version":1,"delegate":[]}`
	if err := os.WriteFile(filepath.Join(root, configName), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveInvocation(root, true, []string{"doctor"}); err == nil {
		t.Fatal("expected empty delegate to fail")
	}
}

func TestResolveInvocationRejectsUnsupportedVersion(t *testing.T) {
	root := t.TempDir()
	cfg := `{"version":2,"delegate":["node","scripts/devctl.mjs"]}`
	if err := os.WriteFile(filepath.Join(root, configName), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveInvocation(root, true, []string{"doctor"}); err == nil {
		t.Fatal("expected unsupported config version to fail")
	}
}

func TestRunPreservesDelegateExitCode(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"version":1,"delegate":["sh","-c","exit 8","devx-test"]}`
	if err := os.WriteFile(filepath.Join(root, configName), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	err = run([]string{"checks", "TASK-1"})
	if got := exitCode(err); got != 8 {
		t.Fatalf("exitCode(run()) = %d; want 8 (err=%v)", got, err)
	}
}
