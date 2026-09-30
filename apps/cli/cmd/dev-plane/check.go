package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/repo-manifest"
)

const defaultCheckTimeout = 5 * time.Minute

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	changed := fs.Bool("changed", false, "validate only checks affected by changed files")
	repositoryPath := fs.String("path", ".", "repository root containing dev-plane.json")
	base := fs.String("base", "", "optional base revision for committed changes")
	dryRun := fs.Bool("dry-run", false, "print the validation plan without executing it")
	jsonOutput := fs.Bool("json", false, "print the validation plan as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*changed {
		return fmt.Errorf("--changed is required")
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
		return nil
	}
	for _, check := range plan.Checks {
		if err := runPlannedCheck(*repositoryPath, check); err != nil {
			return err
		}
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
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s check timed out after %s", label, timeout)
		}
		return fmt.Errorf("%s check failed: %w", label, err)
	}
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
