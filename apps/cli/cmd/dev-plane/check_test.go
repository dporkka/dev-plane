package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunCheckChangedExecutesAffectedComponentChecks(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.test")
	runGit(t, repo, "config", "user.name", "Dev Plane Test")

	mustWriteFile(t, filepath.Join(repo, "packages", "core", "core.txt"), "baseline\n")
	mustWriteFile(t, filepath.Join(repo, "apps", "cli", "cli.txt"), "baseline\n")
	mustWriteFile(t, filepath.Join(repo, "dev-plane.json"), `{
		"schema_version": 1,
		"commands": {
			"test": {"run": "printf fallback > .fallback-checked"}
		},
		"validation": {
			"fallback_checks": ["test"]
		},
		"components": {
			"core": {
				"paths": ["packages/core/**"],
				"checks": {
					"test": {"run": "printf core > .core-checked"}
				}
			},
			"cli": {
				"paths": ["apps/cli/**"],
				"depends_on": ["core"],
				"checks": {
					"test": {"run": "printf cli > .cli-checked"}
				}
			}
		}
	}`)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "baseline")

	mustWriteFile(t, filepath.Join(repo, "packages", "core", "core.txt"), "changed\n")

	if err := runCheck([]string{"--changed", "--path", repo}); err != nil {
		t.Fatalf("runCheck: %v", err)
	}
	for _, marker := range []string{".core-checked", ".cli-checked"} {
		if _, err := os.Stat(filepath.Join(repo, marker)); err != nil {
			t.Fatalf("expected %s to be created: %v", marker, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".fallback-checked")); !os.IsNotExist(err) {
		t.Fatalf("fallback check ran for mapped change; stat error = %v", err)
	}
}

func TestGitChangedFilesIncludesUntrackedFiles(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.test")
	runGit(t, repo, "config", "user.name", "Dev Plane Test")
	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "baseline\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "baseline")

	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	mustWriteFile(t, filepath.Join(repo, "untracked.txt"), "new\n")

	files, err := gitChangedFiles(repo, "")
	if err != nil {
		t.Fatalf("gitChangedFiles: %v", err)
	}
	if !containsString(files, "tracked.txt") {
		t.Fatalf("changed files = %#v, want tracked.txt", files)
	}
	if !containsString(files, "untracked.txt") {
		t.Fatalf("changed files = %#v, want untracked.txt", files)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
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
