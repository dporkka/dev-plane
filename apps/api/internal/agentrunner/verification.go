package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/readiness"
	"github.com/ai-dev-control-plane/runtimes"
)

func (r *Runner) loadVerificationPlan(ctx context.Context, repositoryID string) (readiness.VerificationPlan, error) {
	if r == nil || r.db == nil {
		return readiness.VerificationPlan{}, nil
	}
	var testCommand, lintCommand, typecheckCommand, buildCommand sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT test_command, lint_command, typecheck_command, build_command
		FROM project_configs
		WHERE repository_id = $1
		ORDER BY updated_at DESC
		LIMIT 1
	`, strings.TrimSpace(repositoryID)).Scan(
		&testCommand,
		&lintCommand,
		&typecheckCommand,
		&buildCommand,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return readiness.VerificationPlan{}, nil
	}
	if err != nil {
		return readiness.VerificationPlan{}, fmt.Errorf("load verification plan: %w", err)
	}
	return readiness.BuildVerificationPlan(readiness.VerificationCommands{
		Test:      testCommand.String,
		Lint:      lintCommand.String,
		Typecheck: typecheckCommand.String,
		Build:     buildCommand.String,
	}), nil
}

func (r *Runner) runConfiguredFinalChecks(
	ctx context.Context,
	run *models.AgentRun,
	task *models.Task,
	workspace *models.Workspace,
	workspacePath string,
) map[string]any {
	results := make(map[string]any)
	if task == nil || workspace == nil {
		results["error"] = "task and workspace are required for final checks"
		return results
	}

	plan, err := r.loadVerificationPlan(ctx, task.RepositoryID)
	if err != nil {
		results["error"] = err.Error()
		return results
	}
	if len(plan.Checks) == 0 {
		results["evidence"] = []map[string]any{}
		results["passed"] = true
		return results
	}

	subjectRevision, err := r.workspaceSubjectRevision(ctx, workspace, workspacePath)
	if err != nil {
		results["error"] = err.Error()
		return results
	}
	results["subject_revision"] = subjectRevision

	evidence := make([]map[string]any, 0, len(plan.Checks))
	for _, check := range plan.Checks {
		status, _, durationMs, checkErr := r.executeVerificationCheck(
			ctx,
			run,
			task,
			workspace,
			workspacePath,
			check,
		)
		item := map[string]any{
			"name":             check.Name,
			"kind":             "command",
			"status":           status,
			"command":          check.Command,
			"subject_revision": subjectRevision,
			"duration_ms":      durationMs,
		}
		if checkErr != nil {
			item["error"] = checkErr.Error()
		}
		evidence = append(evidence, item)
	}

	afterRevision, err := r.workspaceSubjectRevision(ctx, workspace, workspacePath)
	if err != nil {
		results["error"] = err.Error()
		return results
	}
	if afterRevision != subjectRevision {
		for _, item := range evidence {
			item["status"] = "failed"
			item["error"] = fmt.Sprintf(
				"workspace changed during verification: before %s after %s",
				subjectRevision,
				afterRevision,
			)
		}
		results["workspace_changed_during_verification"] = true
	}
	results["evidence"] = evidence
	results["passed"] = verificationEvidencePassed(evidence)
	return results
}

func verificationEvidencePassed(evidence []map[string]any) bool {
	for _, item := range evidence {
		if status, _ := item["status"].(string); status != "passed" {
			return false
		}
	}
	return true
}

func (r *Runner) executeVerificationCheck(
	ctx context.Context,
	run *models.AgentRun,
	task *models.Task,
	workspace *models.Workspace,
	workspacePath string,
	check readiness.VerificationCheck,
) (status, output string, durationMs int, err error) {
	start := time.Now()
	defer func() {
		durationMs = int(time.Since(start).Milliseconds())
	}()

	input, err := json.Marshal(map[string]any{
		"command": check.Command,
		"timeout": 600,
	})
	if err != nil {
		return "failed", "", 0, err
	}

	toolName := "run_command"
	if check.Name == "tests" {
		toolName = "run_tests"
	}
	raw, err := r.executeTool(ctx, run, task, workspace, workspacePath, toolName, input)
	if err != nil {
		return "failed", "", 0, err
	}

	var payload struct {
		Passed   *bool  `json:"passed,omitempty"`
		ExitCode int    `json:"exit_code"`
		Output   string `json:"output,omitempty"`
		Stdout   string `json:"stdout,omitempty"`
		Stderr   string `json:"stderr,omitempty"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "failed", string(raw), 0, fmt.Errorf("decode verification result: %w", err)
	}
	passed := payload.ExitCode == 0
	if payload.Passed != nil {
		passed = *payload.Passed
	}
	if passed {
		status = "passed"
	} else {
		status = "failed"
	}
	output = payload.Output
	if output == "" {
		output = strings.TrimSpace(payload.Stdout + "\n" + payload.Stderr)
	}
	return status, output, 0, nil
}

func (r *Runner) workspaceSubjectRevision(
	ctx context.Context,
	workspace *models.Workspace,
	workspacePath string,
) (string, error) {
	provider, sessionID, err := r.runtimeProviderForWorkspace(ctx, workspace)
	if err != nil {
		return "", err
	}
	if provider == nil {
		return localWorkspaceSubjectRevision(ctx, workspacePath)
	}
	return runtimeWorkspaceSubjectRevision(ctx, provider, sessionID)
}

func localWorkspaceSubjectRevision(ctx context.Context, workspacePath string) (string, error) {
	index, err := os.CreateTemp("", "dev-plane-verification-index-*")
	if err != nil {
		return "", fmt.Errorf("create temporary git index: %w", err)
	}
	indexPath := index.Name()
	if err := index.Close(); err != nil {
		_ = os.Remove(indexPath)
		return "", err
	}
	_ = os.Remove(indexPath)
	defer os.Remove(indexPath)

	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workspacePath}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return strings.TrimSpace(string(out)), nil
	}
	if _, err := run("read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := run("add", "-A"); err != nil {
		return "", err
	}
	tree, err := run("write-tree")
	if err != nil {
		return "", err
	}
	if tree == "" {
		return "", errors.New("git write-tree returned an empty revision")
	}
	return "tree:" + tree, nil
}

func runtimeWorkspaceSubjectRevision(
	ctx context.Context,
	provider runtimes.Provider,
	sessionID string,
) (string, error) {
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Command:     `tmp=$(mktemp); trap 'rm -f "$tmp"' EXIT; export GIT_INDEX_FILE="$tmp"; git read-tree HEAD && git add -A && git write-tree`,
		Timeout:     30 * time.Second,
		UnsafeShell: true,
	})
	if err != nil {
		return "", fmt.Errorf("runtime workspace revision: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("runtime workspace revision exited %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	tree := strings.TrimSpace(result.Stdout)
	if tree == "" {
		return "", errors.New("runtime git write-tree returned an empty revision")
	}
	return "tree:" + tree, nil
}

func truncateVerificationOutput(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "... [truncated]"
}
