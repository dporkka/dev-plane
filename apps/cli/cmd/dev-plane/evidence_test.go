package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	verification "github.com/ai-dev-control-plane/verification"
)

func TestRunEvidenceVerifyAcceptsFreshArtifactAndRejectsNewTree(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.test")
	runGit(t, repo, "config", "user.name", "Dev Plane Test")
	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "baseline\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "baseline")

	artifactPath := filepath.Join(t.TempDir(), "evidence.json")
	writeTestArtifact(t, repo, artifactPath, "env-a")

	if err := runEvidenceVerify([]string{"--path", repo, "--environment-digest", "env-a", artifactPath}); err != nil {
		t.Fatalf("runEvidenceVerify fresh: %v", err)
	}

	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "changed\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "change")

	err := runEvidenceVerify([]string{"--path", repo, "--environment-digest", "env-a", artifactPath})
	if err == nil || !strings.Contains(err.Error(), "stale: tree hash changed") {
		t.Fatalf("runEvidenceVerify changed tree error = %v", err)
	}
}

func TestRunEvidenceVerifyRejectsDifferentEnvironment(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "dev-plane@example.test")
	runGit(t, repo, "config", "user.name", "Dev Plane Test")
	mustWriteFile(t, filepath.Join(repo, "tracked.txt"), "baseline\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "baseline")

	artifactPath := filepath.Join(t.TempDir(), "evidence.json")
	writeTestArtifact(t, repo, artifactPath, "env-a")

	err := runEvidenceVerify([]string{"--path", repo, "--environment-digest", "env-b", artifactPath})
	if err == nil || !strings.Contains(err.Error(), "stale: environment changed") {
		t.Fatalf("runEvidenceVerify environment error = %v", err)
	}
}

func writeTestArtifact(t *testing.T, repo, outputPath, environmentDigest string) {
	t.Helper()
	contract := verification.Contract{
		Version: verification.ContractVersion,
		Checks: []verification.Check{{
			ID:       "test",
			Command:  "true",
			Required: true,
		}},
	}
	treeHash := strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "HEAD^{tree}"))
	started := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	evidence, err := verification.NewEvidence(verification.EvidenceInput{
		TreeHash:          treeHash,
		Contract:          contract,
		EnvironmentDigest: environmentDigest,
		RunnerIdentity:    "runner-test",
		Checks:            []verification.CheckResult{{ID: "test", Passed: true, ExitCode: 0}},
		StartedAt:         started,
		CompletedAt:       started.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := verification.Artifact{
		Version:  verification.ArtifactVersion,
		Contract: contract,
		Evidence: evidence,
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
