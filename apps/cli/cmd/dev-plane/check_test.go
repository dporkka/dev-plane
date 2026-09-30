package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	verification "github.com/ai-dev-control-plane/verification"
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


func TestRunCheckChangedWritesEvidenceForCommittedCandidate(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.test")
	runGit(t, repo, "config", "user.name", "Dev Plane Test")

	mustWriteFile(t, filepath.Join(repo, "packages", "core", "core.txt"), "baseline\n")
	mustWriteFile(t, filepath.Join(repo, "dev-plane.json"), `{
		"schema_version": 1,
		"commands": {
			"test": {"run": "printf verified > .verified"}
		},
		"validation": {
			"fallback_checks": ["test"]
		},
		"components": {
			"core": {
				"paths": ["packages/core/**"],
				"checks": {
					"test": {"run": "printf verified > .verified"}
				}
			}
		}
	}`)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "baseline")
	base := strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "HEAD"))

	mustWriteFile(t, filepath.Join(repo, "packages", "core", "core.txt"), "changed\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "change")

	evidencePath := filepath.Join(t.TempDir(), "evidence.json")
	err := runCheck([]string{
		"--changed",
		"--path", repo,
		"--base", base,
		"--evidence-out", evidencePath,
		"--environment-digest", "env-test",
		"--runner-id", "runner-test",
	})
	if err != nil {
		t.Fatalf("runCheck: %v", err)
	}

	data, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	var artifact verification.Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatalf("decode evidence artifact: %v", err)
	}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("validate evidence artifact: %v", err)
	}
	evidence := artifact.Evidence
	wantTree := strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "HEAD^{tree}"))
	if evidence.TreeHash != wantTree {
		t.Fatalf("TreeHash = %q, want %q", evidence.TreeHash, wantTree)
	}
	if evidence.EnvironmentDigest != "env-test" {
		t.Fatalf("EnvironmentDigest = %q", evidence.EnvironmentDigest)
	}
	if evidence.RunnerIdentity != "runner-test" {
		t.Fatalf("RunnerIdentity = %q", evidence.RunnerIdentity)
	}
	if artifact.Contract.Scope == nil || len(artifact.Contract.Scope.AffectedComponents) != 1 || artifact.Contract.Scope.AffectedComponents[0] != "core" {
		t.Fatalf("Contract.Scope = %#v", artifact.Contract.Scope)
	}
	if len(evidence.Checks) != 1 || evidence.Checks[0].ID != "core:test" || !evidence.Checks[0].Passed {
		t.Fatalf("Checks = %#v", evidence.Checks)
	}
}

func TestRunCheckEvidenceRefusesDirtyCandidate(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.test")
	runGit(t, repo, "config", "user.name", "Dev Plane Test")
	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "baseline\n")
	mustWriteFile(t, filepath.Join(repo, "dev-plane.json"), `{
		"schema_version": 1,
		"commands": {
			"test": {"run": "true"}
		},
		"validation": {
			"fallback_checks": ["test"]
		}
	}`)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "baseline")
	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "dirty\n")

	evidencePath := filepath.Join(repo, "evidence.json")
	err := runCheck([]string{
		"--changed",
		"--path", repo,
		"--evidence-out", evidencePath,
		"--environment-digest", "env-test",
		"--runner-id", "runner-test",
	})
	if err == nil || !strings.Contains(err.Error(), "clean committed candidate") {
		t.Fatalf("runCheck error = %v, want dirty candidate rejection", err)
	}
	if _, statErr := os.Stat(evidencePath); !os.IsNotExist(statErr) {
		t.Fatalf("evidence file should not exist; stat error = %v", statErr)
	}
}

func TestRunCheckFailureDoesNotWriteEvidence(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.test")
	runGit(t, repo, "config", "user.name", "Dev Plane Test")
	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "baseline\n")
	mustWriteFile(t, filepath.Join(repo, "dev-plane.json"), `{
		"schema_version": 1,
		"commands": {
			"test": {"run": "exit 7"}
		},
		"validation": {
			"fallback_checks": ["test"]
		}
	}`)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "baseline")
	base := strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "HEAD"))
	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "change")

	evidencePath := filepath.Join(repo, "evidence.json")
	err := runCheck([]string{
		"--changed",
		"--path", repo,
		"--base", base,
		"--evidence-out", evidencePath,
		"--environment-digest", "env-test",
		"--runner-id", "runner-test",
	})
	if err == nil {
		t.Fatal("runCheck error = nil, want failed check")
	}
	if _, statErr := os.Stat(evidencePath); !os.IsNotExist(statErr) {
		t.Fatalf("evidence file should not exist; stat error = %v", statErr)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}
