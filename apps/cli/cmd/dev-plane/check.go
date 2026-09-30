package main

import (
	"context"
	"errors"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	repomanifest "github.com/ai-dev-control-plane/repo-manifest"
	verification "github.com/ai-dev-control-plane/verification"
)

const defaultCheckTimeout = 5 * time.Minute

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	changed := fs.Bool("changed", false, "validate only checks affected by changed files")
	repositoryPath := fs.String("path", ".", "repository root containing dev-plane.json")
	base := fs.String("base", "", "optional base revision for committed changes")
	dryRun := fs.Bool("dry-run", false, "print the validation plan without executing it")
	jsonOutput := fs.Bool("json", false, "print the validation plan as JSON")
	evidenceOut := fs.String("evidence-out", "", "optional path for a self-contained verification evidence artifact")
	environmentDigest := fs.String("environment-digest", os.Getenv("DEV_PLANE_ENVIRONMENT_DIGEST"), "stable digest identifying the verification environment")
	runnerID := fs.String("runner-id", os.Getenv("DEV_PLANE_RUNNER_ID"), "identity of the verification runner")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*changed {
		return fmt.Errorf("--changed is required")
	}
	if *evidenceOut != "" {
		if *dryRun {
			return fmt.Errorf("--evidence-out cannot be used with --dry-run")
		}
		if strings.TrimSpace(*environmentDigest) == "" {
			return fmt.Errorf("--environment-digest or DEV_PLANE_ENVIRONMENT_DIGEST is required with --evidence-out")
		}
		if strings.TrimSpace(*runnerID) == "" {
			return fmt.Errorf("--runner-id or DEV_PLANE_RUNNER_ID is required with --evidence-out")
		}
		if err := requireCleanCommittedCandidate(*repositoryPath); err != nil {
			return err
		}
	}

	manifest, err := repomanifest.Load(*repositoryPath)
	if err != nil {
		return fmt.Errorf("load repository manifest: %w", err)
	}
	files, err := gitChangedFiles(*repositoryPath, *base)
	if err != nil {
		return err
	}
	plan, err := manifest.PlanChecks(files)
	if err != nil {
		return fmt.Errorf("plan changed validation: %w", err)
	}

	if *jsonOutput {
		encoded, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return fmt.Errorf("encode validation plan: %w", err)
		}
		fmt.Println(string(encoded))
	} else {
		printCheckPlan(plan)
	}

	if *dryRun || len(plan.Checks) == 0 {
		if *evidenceOut != "" {
			return fmt.Errorf("cannot write verification evidence: validation plan contains no checks")
		}
		return nil
	}

	var (
		contract       verification.Contract
		treeHash       string
		startedAt      time.Time
		checkResults   []verification.CheckResult
	)
	if *evidenceOut != "" {
		contract, err = verification.ContractFromPlan(plan)
		if err != nil {
			return fmt.Errorf("derive verification contract: %w", err)
		}
		treeHash, err = gitTreeHash(*repositoryPath)
		if err != nil {
			return err
		}
		startedAt = time.Now().UTC()
		checkResults = make([]verification.CheckResult, 0, len(plan.Checks))
	}

	for _, check := range plan.Checks {
		result, err := executePlannedCheck(*repositoryPath, check)
		if err != nil {
			return err
		}
		if *evidenceOut != "" {
			checkResults = append(checkResults, result)
		}
	}

	if *evidenceOut == "" {
		return nil
	}
	if err := requireCleanCommittedCandidate(*repositoryPath); err != nil {
		return fmt.Errorf("candidate changed during verification: %w", err)
	}
	currentTree, err := gitTreeHash(*repositoryPath)
	if err != nil {
		return err
	}
	if currentTree != treeHash {
		return fmt.Errorf("candidate tree changed during verification: %s -> %s", treeHash, currentTree)
	}

	evidence, err := verification.NewEvidence(verification.EvidenceInput{
		TreeHash:          treeHash,
		Contract:          contract,
		EnvironmentDigest: strings.TrimSpace(*environmentDigest),
		RunnerIdentity:    strings.TrimSpace(*runnerID),
		Checks:            checkResults,
		StartedAt:         startedAt,
		CompletedAt:       time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("create verification evidence: %w", err)
	}
	artifact := verification.Artifact{
		Version:  verification.ArtifactVersion,
		Contract: contract,
		Evidence: evidence,
	}
	if err := artifact.Validate(); err != nil {
		return fmt.Errorf("validate verification artifact: %w", err)
	}
	if err := writeEvidenceArtifact(*repositoryPath, *evidenceOut, artifact); err != nil {
		return err
	}
	return nil
}

func gitChangedFiles(repositoryPath, base string) ([]string, error) {
	var files []string
	if base != "" {
		committed, err := gitNameOnly(repositoryPath, "diff", "--name-only", "--diff-filter=ACMR", base+"...HEAD")
		if err != nil {
			return nil, fmt.Errorf("list changes from %s: %w", base, err)
		}
		files = append(files, committed...)
	}

	working, err := gitNameOnly(repositoryPath, "diff", "--name-only", "--diff-filter=ACMR", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("list working tree changes: %w", err)
	}
	files = append(files, working...)

	untracked, err := gitNameOnly(repositoryPath, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w", err)
	}
	files = append(files, untracked...)

	seen := make(map[string]bool, len(files))
	unique := make([]string, 0, len(files))
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" || seen[file] {
			continue
		}
		seen[file] = true
		unique = append(unique, file)
	}
	sort.Strings(unique)
	return unique, nil
}

func gitNameOnly(repositoryPath string, args ...string) ([]string, error) {
	cmd := exec.Command("git", append([]string{"-C", repositoryPath}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	if len(output) == 0 {
		return nil, nil
	}
	return strings.Split(strings.TrimSpace(string(output)), "\n"), nil
}

func runPlannedCheck(repositoryPath string, check repomanifest.PlannedCheck) error {
	_, err := executePlannedCheck(repositoryPath, check)
	return err
}

func executePlannedCheck(repositoryPath string, check repomanifest.PlannedCheck) (verification.CheckResult, error) {
	timeout := defaultCheckTimeout
	if check.Command.TimeoutSeconds > 0 {
		timeout = time.Duration(check.Command.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	label := check.Kind
	if check.Component != "" {
		label = check.Component + ":" + check.Kind
	}
	fmt.Printf("==> %s: %s\n", label, check.Command.Run)

	cmd := exec.CommandContext(ctx, "sh", "-c", check.Command.Run)
	cmd.Dir = repositoryPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		exitCode := 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		result := verification.CheckResult{ID: label, Passed: false, ExitCode: exitCode}
		if ctx.Err() == context.DeadlineExceeded {
			return result, fmt.Errorf("%s check timed out after %s", label, timeout)
		}
		return result, fmt.Errorf("%s check failed: %w", label, err)
	}
	return verification.CheckResult{ID: label, Passed: true, ExitCode: 0}, nil
}

func requireCleanCommittedCandidate(repositoryPath string) error {
	cmd := exec.Command("git", "-C", repositoryPath, "status", "--porcelain=v1", "--untracked-files=all")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect candidate cleanliness: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) != "" {
		return fmt.Errorf("verification evidence requires a clean committed candidate")
	}
	return nil
}

func gitTreeHash(repositoryPath string) (string, error) {
	cmd := exec.Command("git", "-C", repositoryPath, "rev-parse", "HEAD^{tree}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve candidate tree hash: %w: %s", err, strings.TrimSpace(string(output)))
	}
	treeHash := strings.TrimSpace(string(output))
	if treeHash == "" {
		return "", fmt.Errorf("resolve candidate tree hash: empty result")
	}
	return treeHash, nil
}

func writeEvidenceArtifact(repositoryPath, outputPath string, artifact verification.Artifact) error {
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(repositoryPath, outputPath)
	}
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".dev-plane-evidence-*")
	if err != nil {
		return fmt.Errorf("create evidence temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(artifact); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("encode verification artifact: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync verification artifact: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod verification artifact: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close verification artifact: %w", err)
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("publish verification artifact: %w", err)
	}
	cleanup = false
	return nil
}

func printCheckPlan(plan repomanifest.CheckPlan) {
	fmt.Printf("changed files: %d\n", len(plan.ChangedFiles))
	if len(plan.ChangedComponents) > 0 {
		fmt.Printf("changed components: %s\n", strings.Join(plan.ChangedComponents, ", "))
	}
	if len(plan.AffectedComponents) > 0 {
		fmt.Printf("affected components: %s\n", strings.Join(plan.AffectedComponents, ", "))
	}
	if len(plan.Checks) == 0 {
		fmt.Println("checks: none")
		return
	}
	fmt.Printf("checks: %d\n", len(plan.Checks))
	for _, check := range plan.Checks {
		label := check.Kind
		if check.Component != "" {
			label = check.Component + ":" + check.Kind
		}
		fmt.Printf("  - %s -> %s\n", label, check.Command.Run)
	}
}
