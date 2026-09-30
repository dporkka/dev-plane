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
	"github.com/ai-dev-control-plane/scheduler"
)

// RunCompletionObserver persists authority-bearing verification evidence before
// a run is allowed to transition to completed.
type RunCompletionObserver interface {
	RecordRunCompletion(
		ctx context.Context,
		runID string,
		subjectRevision string,
		evidence []scheduler.Evidence,
	) error
}

type finalCheckReport struct {
	Passed          bool
	Results         map[string]any
	Evidence        []scheduler.Evidence
	SubjectRevision string
}

func (r *Runner) WithCompletionObserver(observer RunCompletionObserver) *Runner {
	if r != nil {
		r.completionObserver = observer
	}
	return r
}

func (r *Runner) executeFinalChecks(
	ctx context.Context,
	run *models.AgentRun,
	task *models.Task,
	workspace *models.Workspace,
	workspacePath string,
) (finalCheckReport, error) {
	plan, err := r.loadFinalVerificationPlan(ctx, task, workspace)
	if err != nil {
		return finalCheckReport{}, err
	}

	bindRevision := r.completionObserver != nil
	beforeRevision := ""
	if bindRevision {
		beforeRevision, err = r.workspaceSubjectRevision(ctx, workspace, workspacePath)
		if err != nil {
			return finalCheckReport{}, fmt.Errorf("capture pre-verification subject revision: %w", err)
		}
	}

	report := finalCheckReport{
		Passed:   true,
		Results:  make(map[string]any, len(plan.Checks)),
		Evidence: make([]scheduler.Evidence, 0, len(plan.Checks)),
	}
	for _, step := range plan.Checks {
		toolName := "run_command"
		if step.Name == "tests" {
			toolName = "run_tests"
		}
		input := json.RawMessage(`{}`)
		if step.Command != "" {
			encoded, err := json.Marshal(map[string]any{"command": step.Command})
			if err != nil {
				return finalCheckReport{}, fmt.Errorf("encode %s verification command: %w", step.Name, err)
			}
			input = encoded
		}

		output, toolErr := r.executeTool(ctx, run, task, workspace, workspacePath, toolName, input)
		passed, detail, parseErr := decodeFinalCheckResult(toolName, output, toolErr)
		if parseErr != nil {
			passed = false
			if detail == "" {
				detail = parseErr.Error()
			} else {
				detail += "; " + parseErr.Error()
			}
		}
		if !passed {
			report.Passed = false
		}
		status := scheduler.EvidenceStatusFailed
		if passed {
			status = scheduler.EvidenceStatusPassed
		}
		report.Evidence = append(report.Evidence, scheduler.Evidence{
			Name:    step.Name,
			Kind:    "command",
			Status:  status,
			Command: step.Command,
		})
		report.Results[step.Name] = map[string]any{
			"passed":  passed,
			"command": step.Command,
			"detail":  detail,
		}
	}

	if bindRevision {
		afterRevision, err := r.workspaceSubjectRevision(ctx, workspace, workspacePath)
		if err != nil {
			return finalCheckReport{}, fmt.Errorf("capture post-verification subject revision: %w", err)
		}
		if beforeRevision != afterRevision {
			return finalCheckReport{}, fmt.Errorf(
				"workspace changed during final verification: revision %s became %s",
				beforeRevision,
				afterRevision,
			)
		}
		report.SubjectRevision = afterRevision
		for i := range report.Evidence {
			report.Evidence[i].SubjectRevision = afterRevision
		}
	}
	return report, nil
}

func (r *Runner) loadFinalVerificationPlan(
	ctx context.Context,
	task *models.Task,
	workspace *models.Workspace,
) (readiness.VerificationPlan, error) {
	repositoryID := ""
	if task != nil {
		repositoryID = strings.TrimSpace(task.RepositoryID)
	}
	if repositoryID == "" && workspace != nil {
		repositoryID = strings.TrimSpace(workspace.RepositoryID)
	}
	if r == nil || r.db == nil || repositoryID == "" {
		return readiness.VerificationPlan{}, nil
	}

	var testCommand, lintCommand, typecheckCommand, buildCommand sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT test_command, lint_command, typecheck_command, build_command
		FROM project_configs
		WHERE repository_id = $1
		ORDER BY updated_at DESC
		LIMIT 1
	`, repositoryID).Scan(&testCommand, &lintCommand, &typecheckCommand, &buildCommand)
	if errors.Is(err, sql.ErrNoRows) {
		return readiness.VerificationPlan{}, nil
	}
	if err != nil {
		// Older/minimal runner contexts may not have the detection schema. They
		// retain the legacy auto-detected test path unless a durable completion
		// observer is installed, in which case missing configuration is an
		// authority-path error and must fail closed.
		if r.completionObserver == nil && strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return readiness.VerificationPlan{}, nil
		}
		return readiness.VerificationPlan{}, fmt.Errorf("load final verification commands: %w", err)
	}
	return readiness.BuildVerificationPlan(readiness.VerificationCommands{
		Test:      testCommand.String,
		Lint:      lintCommand.String,
		Typecheck: typecheckCommand.String,
		Build:     buildCommand.String,
	}), nil
}

func decodeFinalCheckResult(toolName string, output json.RawMessage, toolErr error) (bool, string, error) {
	if toolErr != nil {
		return false, toolErr.Error(), nil
	}
	if len(output) == 0 {
		return false, "", errors.New("verification tool returned no output")
	}

	switch toolName {
	case "run_tests":
		var result struct {
			Passed   bool   `json:"passed"`
			ExitCode int    `json:"exit_code"`
			Output   string `json:"output"`
		}
		if err := json.Unmarshal(output, &result); err != nil {
			return false, "", fmt.Errorf("decode test verification result: %w", err)
		}
		return result.Passed && result.ExitCode == 0, strings.TrimSpace(result.Output), nil
	case "run_command":
		var result struct {
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
			ExitCode int    `json:"exit_code"`
		}
		if err := json.Unmarshal(output, &result); err != nil {
			return false, "", fmt.Errorf("decode command verification result: %w", err)
		}
		detail := strings.TrimSpace(strings.TrimSpace(result.Stdout) + "\n" + strings.TrimSpace(result.Stderr))
		return result.ExitCode == 0, detail, nil
	default:
		return false, "", fmt.Errorf("unsupported verification tool %q", toolName)
	}
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
	if provider != nil {
		return runtimeSubjectRevision(ctx, provider, sessionID)
	}
	return localSubjectRevision(ctx, workspacePath)
}

func runtimeSubjectRevision(ctx context.Context, provider runtimes.Provider, sessionID string) (string, error) {
	const command = `tmp=$(mktemp); rm -f "$tmp"; trap 'rm -f "$tmp"' EXIT; GIT_INDEX_FILE="$tmp" git read-tree HEAD >/dev/null && GIT_INDEX_FILE="$tmp" git add -A >/dev/null && GIT_INDEX_FILE="$tmp" git write-tree`
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Command:     command,
		Timeout:     30 * time.Second,
		UnsafeShell: true,
	})
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", errors.New("workspace revision command returned no result")
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("workspace revision command failed: %s", strings.TrimSpace(result.Stdout+result.Stderr))
	}
	revision := strings.TrimSpace(result.Stdout)
	if revision == "" {
		return "", errors.New("workspace revision is empty")
	}
	return "git-tree:" + revision, nil
}

func localSubjectRevision(ctx context.Context, workspacePath string) (string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", errors.New("workspace path is required")
	}

	indexFile, err := os.CreateTemp("", "dev-plane-verification-index-*")
	if err != nil {
		return "", fmt.Errorf("create temporary git index: %w", err)
	}
	indexPath := indexFile.Name()
	if err := indexFile.Close(); err != nil {
		_ = os.Remove(indexPath)
		return "", fmt.Errorf("close temporary git index: %w", err)
	}
	if err := os.Remove(indexPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("prepare temporary git index: %w", err)
	}
	defer os.Remove(indexPath)

	runGit := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workspacePath
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+indexPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
		return output, nil
	}
	if _, err := runGit("read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := runGit("add", "-A"); err != nil {
		return "", err
	}
	output, err := runGit("write-tree")
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(string(output))
	if revision == "" {
		return "", errors.New("workspace revision is empty")
	}
	return "git-tree:" + revision, nil
}
