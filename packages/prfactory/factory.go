// Package prfactory creates pull requests for completed agent tasks.
//
// The Factory loads task data, review reports, and workspace information to build
// comprehensive PR descriptions and create GitHub pull requests.
package prfactory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
)

type githubPRCreator interface {
	CreatePR(ctx context.Context, token *oauth2.Token, owner, name string, pr gateway.NewPR) (*gateway.GitHubPR, error)
}

// shortID returns the first n bytes of id, or the full id if shorter.
func shortID(id string, n int) string {
	if len(id) <= n {
		return id
	}
	return id[:n]
}

// Factory creates pull requests for completed tasks.
type Factory struct {
	db          *sql.DB
	logger      *slog.Logger
	github      githubPRCreator
	githubToken string
}

// NewFactory creates a PR factory.
func NewFactory(db *sql.DB, logger *slog.Logger) *Factory {
	if logger == nil {
		logger = slog.Default()
	}
	f := &Factory{
		db:          db,
		logger:      logger,
		githubToken: strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
	}
	if f.githubToken != "" {
		f.github = gateway.NewGitHubGateway(os.Getenv("GITHUB_CLIENT_ID"), os.Getenv("GITHUB_CLIENT_SECRET"))
	}
	return f
}

// WithGitHubGateway adds a GitHub gateway for creating actual PRs.
func (f *Factory) WithGitHubGateway(gh *gateway.GitHubGateway) *Factory {
	f.github = gh
	return f
}

// WithGitHubToken configures the token used for branch pushes and GitHub PR creation.
func (f *Factory) WithGitHubToken(token string) *Factory {
	f.githubToken = strings.TrimSpace(token)
	return f
}

// CreatePullRequest opens a GitHub PR for completed task changes.
//
// Steps:
//  1. Load task, workspace, agent run from DB
//  2. Verify run status is "completed" or "reviewed"
//  3. Get git diff and review report
//  4. Build comprehensive PR body
//  5. Push branch to origin (if not already pushed)
//  6. Create PR via GitHub API
//  7. Save PR record in DB
//  8. Update task status to "pr_created"
//  9. Publish pr.created event
func (f *Factory) CreatePullRequest(ctx context.Context, taskID string) (*models.PullRequest, error) {
	f.logger.Info("creating pull request", "task_id", taskID)

	// 1. Load task
	task, err := f.loadTask(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load task: %w", err)
	}

	// 2. Load the latest completed agent run for this task
	run, err := f.loadLatestRun(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("load agent run: %w", err)
	}
	if run == nil {
		return nil, fmt.Errorf("no completed agent run found for task %s", taskID)
	}

	// Verify run status
	if run.Status != models.AgentRunStatusCompleted && run.Status != "reviewed" {
		return nil, fmt.Errorf("agent run status is %q, expected completed or reviewed", run.Status)
	}

	// 3. Get review report
	rev := reviewer.NewReviewer(f.db, f.logger)
	report, err := rev.Get(ctx, run.ID)
	if err != nil {
		f.logger.Warn("no review report found, generating default", "error", err)
		report = &reviewer.ReviewReport{
			RunID:         run.ID,
			Summary:       "Review report not available.",
			RiskLevel:     "medium",
			Approvable:    true,
			Suggestions:   []string{"Manual review recommended."},
			TestCoverage:  "Unknown",
			SecurityNotes: "No automated security scan performed.",
		}
	}

	// 4. Build PR body
	prBody := f.BuildPRBody(task, nil, report, run)

	// 5. Determine branch names
	branch := task.TargetBranch
	if branch == "" {
		branch = "main"
	}

	workspaceBranch := branch
	workspacePath := ""
	if run.WorkspaceID != nil {
		ws, err := f.loadWorkspace(ctx, *run.WorkspaceID)
		if err == nil && ws != nil && ws.Branch != "" {
			workspaceBranch = ws.Branch
		}
		if err == nil && ws != nil && ws.WorktreePath != nil {
			workspacePath = strings.TrimSpace(*ws.WorktreePath)
		}
	}

	// 6. Build PR title
	prTitle := fmt.Sprintf("[Agent] %s", task.Title)
	if len(prTitle) > 256 {
		prTitle = prTitle[:253] + "..."
	}

	if f.github == nil {
		return nil, fmt.Errorf("github gateway is not configured; set GITHUB_TOKEN or inject a GitHub gateway")
	}
	if f.githubToken == "" {
		return nil, fmt.Errorf("github token is not configured")
	}

	repoOwner, repoName, err := f.getRepoOwnerName(ctx, task.RepositoryID)
	if err != nil {
		return nil, fmt.Errorf("get repository details: %w", err)
	}
	if workspacePath != "" {
		if err := f.pushBranch(ctx, workspacePath, workspaceBranch); err != nil {
			return nil, fmt.Errorf("push branch %s: %w", workspaceBranch, err)
		}
	}

	draft := report.RiskLevel == "high" || report.RiskLevel == "critical"
	created, err := f.createGitHubPR(ctx, repoOwner, repoName, prTitle, prBody, workspaceBranch, branch, draft)
	if err != nil {
		return nil, fmt.Errorf("create github pull request: %w", err)
	}

	// 8. Create PR record
	pr := &models.PullRequest{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		RunID:      &run.ID,
		RepoID:     task.RepositoryID,
		Number:     created.Number,
		Title:      prTitle,
		Body:       prBody,
		Branch:     workspaceBranch,
		BaseBranch: branch,
		URL:        created.HTMLURL,
		State:      models.PRStateOpen,
		Draft:      draft,
		CreatedBy:  task.CreatedBy,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}

	if err := f.createPRRecord(ctx, pr); err != nil {
		return nil, fmt.Errorf("save PR record: %w", err)
	}

	// 9. Update task status to pr_created
	now := time.Now().UTC()
	_, err = f.db.ExecContext(ctx, `
		UPDATE tasks SET status = 'pr_created', updated_at = $1
		WHERE id = $2 AND deleted_at IS NULL
	`, now, taskID)
	if err != nil {
		f.logger.Warn("failed to update task status to pr_created", "error", err)
	}

	f.logger.Info("pull request created",
		"task_id", taskID,
		"pr_id", pr.ID,
		"pr_number", pr.Number,
		"draft", pr.Draft,
	)

	return pr, nil
}

// BuildPRBody generates a comprehensive PR description.
func (f *Factory) BuildPRBody(task *models.Task, spec *models.TaskSpec, report *reviewer.ReviewReport, run *models.AgentRun) string {
	var b strings.Builder

	// Task section
	b.WriteString("## Task\n\n")
	b.WriteString(fmt.Sprintf("**%s**\n\n", task.Title))
	if task.Description != nil && *task.Description != "" {
		b.WriteString(*task.Description)
		b.WriteString("\n\n")
	}

	// Implementation Summary
	b.WriteString("## Implementation Summary\n\n")
	if run.Summary != nil && *run.Summary != "" {
		b.WriteString(*run.Summary)
		b.WriteString("\n\n")
	} else {
		b.WriteString("This change was generated by an AI agent.\n\n")
	}

	// Changes
	b.WriteString("## Changes\n\n")
	b.WriteString(fmt.Sprintf("- **Files changed:** %d\n", report.DiffSummary.FilesChanged))
	b.WriteString(fmt.Sprintf("- **Insertions:** %d\n", report.DiffSummary.Insertions))
	b.WriteString(fmt.Sprintf("- **Deletions:** %d\n\n", report.DiffSummary.Deletions))

	if len(report.DiffSummary.Files) > 0 {
		b.WriteString("### Files\n\n")
		b.WriteString("| File | Status | +/- |\n")
		b.WriteString("|------|--------|-----|\n")
		for _, f := range report.DiffSummary.Files {
			b.WriteString(fmt.Sprintf("| `%s` | %s | +%d/-%d |\n",
				f.Path, f.Status, f.Insertions, f.Deletions))
		}
		b.WriteString("\n")
	}

	// Review Summary
	if report.Summary != "" {
		b.WriteString("## Review Summary\n\n")
		b.WriteString(report.Summary)
		b.WriteString("\n\n")
	}

	// Findings
	if len(report.Findings) > 0 {
		b.WriteString("## Findings\n\n")
		for _, finding := range report.Findings {
			icon := ""
			switch finding.Severity {
			case "critical":
				icon = "\U0001F534" // red circle
			case "high":
				icon = "\U0001F7E0" // orange circle
			case "medium":
				icon = "\U0001F7E1" // yellow circle
			case "low":
				icon = "\U0001F535" // blue circle
			default:
				icon = "\U0001F518" // info
			}
			b.WriteString(fmt.Sprintf("%s **%s** (%s) - %s\n\n", icon, finding.Severity, finding.Category, finding.Message))
			if finding.Suggestion != "" {
				b.WriteString(fmt.Sprintf("   > %s\n\n", finding.Suggestion))
			}
		}
	}

	// Test Results
	b.WriteString("## Test Results\n\n")
	b.WriteString(fmt.Sprintf("- **Test Coverage:** %s\n", report.TestCoverage))
	b.WriteString(fmt.Sprintf("- **Risk Level:** %s\n", report.RiskLevel))
	b.WriteString(fmt.Sprintf("- **Approvable:** %v\n\n", report.Approvable))

	// Security Review
	b.WriteString("## Security Review\n\n")
	b.WriteString(report.SecurityNotes)
	b.WriteString("\n\n")

	// Known Risks
	b.WriteString("## Known Risks\n\n")
	if report.RiskLevel == "low" {
		b.WriteString("No significant risks identified.\n\n")
	} else {
		b.WriteString(fmt.Sprintf("**Risk Level: %s**\n\n", strings.ToUpper(report.RiskLevel)))
		for _, finding := range report.Findings {
			if finding.Severity == "critical" || finding.Severity == "high" {
				b.WriteString(fmt.Sprintf("- %s\n", finding.Message))
			}
		}
		b.WriteString("\n")
	}

	// Model Usage
	b.WriteString("## Model Usage\n\n")
	model := "unknown"
	provider := "unknown"
	if run.Model != nil {
		model = *run.Model
	}
	if run.Provider != nil {
		provider = *run.Provider
	}
	b.WriteString(fmt.Sprintf("- **Model:** %s\n", model))
	b.WriteString(fmt.Sprintf("- **Provider:** %s\n", provider))
	b.WriteString(fmt.Sprintf("- **Cost:** $%.4f\n", run.TotalCost))
	b.WriteString(fmt.Sprintf("- **Tokens:** %d prompt + %d completion = %d total\n\n",
		run.PromptTokens, run.CompletionTokens, run.PromptTokens+run.CompletionTokens))

	// Rollback
	b.WriteString("## Rollback\n\n")
	b.WriteString(fmt.Sprintf("To revert this change:\n```bash\ngit revert %s-branch\n```\n\n", shortID(task.ID, 8)))

	// Approval Record
	b.WriteString("## Approval Record\n\n")
	b.WriteString("This PR was created by an AI agent and requires human review before merging.\n\n")

	// Run Timeline
	b.WriteString("## Run Timeline\n\n")
	b.WriteString(fmt.Sprintf("[View full run timeline](/runs/%s)\n", run.ID))

	return b.String()
}

// createPRRecord saves PR metadata to DB.
func (f *Factory) createPRRecord(ctx context.Context, pr *models.PullRequest) error {
	if err := pr.Validate(); err != nil {
		return fmt.Errorf("validate PR: %w", err)
	}

	_, err := f.db.ExecContext(ctx, `
		INSERT INTO pull_requests (
			id, task_id, run_id, repository_id, number, title, body,
			branch, base_branch, url, state, draft, created_by, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	`, pr.ID, pr.TaskID, pr.RunID, pr.RepoID, pr.Number, pr.Title, pr.Body,
		pr.Branch, pr.BaseBranch, pr.URL, pr.State, pr.Draft, pr.CreatedBy, pr.CreatedAt, pr.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert pull request: %w", err)
	}
	return nil
}

// pushBranch pushes the workspace branch to origin.
func (f *Factory) pushBranch(ctx context.Context, workspacePath, branch string) error {
	if strings.TrimSpace(workspacePath) == "" {
		return fmt.Errorf("workspace path is required")
	}
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("branch is required")
	}

	cmd := exec.CommandContext(ctx, "git", "-C", workspacePath, "push", "origin", branch)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cleanup, err := configureGitAskPass(cmd, f.githubToken)
	if err != nil {
		return err
	}
	defer cleanup()

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git push failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// createGitHubPR creates a PR via the GitHub API.
func (f *Factory) createGitHubPR(ctx context.Context, owner, name, title, body, head, base string, draft bool) (*gateway.GitHubPR, error) {
	if f.github == nil {
		return nil, fmt.Errorf("github gateway is not configured")
	}
	if f.githubToken == "" {
		return nil, fmt.Errorf("github token is not configured")
	}
	return f.github.CreatePR(ctx, &oauth2.Token{AccessToken: f.githubToken, TokenType: "Bearer"}, owner, name, gateway.NewPR{
		Title: title,
		Body:  body,
		Head:  head,
		Base:  base,
		Draft: draft,
	})
}

func configureGitAskPass(cmd *exec.Cmd, token string) (func(), error) {
	if strings.TrimSpace(token) == "" {
		return func() {}, nil
	}
	dir, err := os.MkdirTemp("", "dev-plane-git-askpass-*")
	if err != nil {
		return nil, fmt.Errorf("create git askpass dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	script := filepath.Join(dir, "askpass.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncase \"$1\" in\n*Username*) printf '%s\\n' x-access-token ;;\n*) printf '%s\\n' \"$GITHUB_TOKEN\" ;;\nesac\n"), 0o700); err != nil {
		cleanup()
		return nil, fmt.Errorf("write git askpass helper: %w", err)
	}
	cmd.Env = append(cmd.Env, "GIT_ASKPASS="+script, "GITHUB_TOKEN="+token)
	return cleanup, nil
}

// loadTask loads a task from the database.
func (f *Factory) loadTask(ctx context.Context, taskID string) (*models.Task, error) {
	var task models.Task
	var desc, sourceID, wsID, spec, ac, approvalReqs, metadata sql.NullString
	var maxCost sql.NullFloat64
	var startedAt, completedAt sql.NullTime

	err := f.db.QueryRowContext(ctx, `
		SELECT id, project_id, repository_id, workspace_id, created_by, source, source_id,
		       title, description, status, priority, risk_level, target_branch,
		       spec, acceptance_criteria, max_cost, max_runtime_minutes,
		       approval_requirements, metadata, started_at, completed_at, created_at, updated_at
		FROM tasks WHERE id = $1 AND deleted_at IS NULL
	`, taskID).Scan(
		&task.ID, &task.ProjectID, &task.RepositoryID, &wsID, &task.CreatedBy, &task.Source, &sourceID,
		&task.Title, &desc, &task.Status, &task.Priority, &task.RiskLevel, &task.TargetBranch,
		&spec, &ac, &maxCost, &task.MaxRuntimeMinutes,
		&approvalReqs, &metadata, &startedAt, &completedAt, &task.CreatedAt, &task.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if desc.Valid {
		task.Description = &desc.String
	}
	if sourceID.Valid {
		s := sourceID.String
		task.SourceID = &s
	}
	if wsID.Valid {
		w := wsID.String
		task.WorkspaceID = &w
	}
	if spec.Valid {
		task.Spec = json.RawMessage(spec.String)
	}
	if ac.Valid {
		task.AcceptanceCriteria = json.RawMessage(ac.String)
	}
	if maxCost.Valid {
		c := maxCost.Float64
		task.MaxCost = &c
	}
	if approvalReqs.Valid {
		task.ApprovalRequirements = json.RawMessage(approvalReqs.String)
	}
	if metadata.Valid {
		task.Metadata = json.RawMessage(metadata.String)
	}
	if startedAt.Valid {
		task.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}

	return &task, nil
}

// loadLatestRun loads the latest completed agent run for a task.
func (f *Factory) loadLatestRun(ctx context.Context, taskID string) (*models.AgentRun, error) {
	var run models.AgentRun
	var wsID, model, provider, errMsg, summary sql.NullString
	var startedAt, completedAt sql.NullTime

	err := f.db.QueryRowContext(ctx, `
		SELECT id, task_id, workspace_id, agent_role, model, provider, status,
		       prompt_tokens, completion_tokens, total_cost, error_message, summary,
		       metadata, created_at, updated_at
		FROM agent_runs
		WHERE task_id = $1 AND status IN ('completed', 'reviewed')
		ORDER BY completed_at IS NULL, completed_at DESC, created_at DESC
		LIMIT 1
	`, taskID).Scan(
		&run.ID, &run.TaskID, &wsID, &run.AgentRole, &model, &provider, &run.Status,
		&run.PromptTokens, &run.CompletionTokens, &run.TotalCost, &errMsg, &summary,
		&run.Metadata, &run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if wsID.Valid {
		w := wsID.String
		run.WorkspaceID = &w
	}
	if model.Valid {
		m := model.String
		run.Model = &m
	}
	if provider.Valid {
		p := provider.String
		run.Provider = &p
	}
	if errMsg.Valid {
		e := errMsg.String
		run.ErrorMessage = &e
	}
	if summary.Valid {
		s := summary.String
		run.Summary = &s
	}
	if startedAt.Valid {
		run.StartedAt = &startedAt.Time
	}
	if completedAt.Valid {
		run.CompletedAt = &completedAt.Time
	}

	return &run, nil
}

// loadWorkspace loads a workspace from the database.
func (f *Factory) loadWorkspace(ctx context.Context, workspaceID string) (*models.Workspace, error) {
	var ws models.Workspace
	var taskID, worktreePath, runtimeSessionID, previewURL, settings sql.NullString
	var deletedAt sql.NullTime

	err := f.db.QueryRowContext(ctx, `
		SELECT id, repository_id, task_id, name, branch, base_branch, worktree_path,
		       runtime_provider, runtime_session_id, status, preview_url, settings,
		       created_at, updated_at, deleted_at
		FROM workspaces WHERE id = $1
	`, workspaceID).Scan(
		&ws.ID, &ws.RepositoryID, &taskID, &ws.Name, &ws.Branch, &ws.BaseBranch, &worktreePath,
		&ws.RuntimeProvider, &runtimeSessionID, &ws.Status, &previewURL, &settings,
		&ws.CreatedAt, &ws.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}
	if taskID.Valid {
		t := taskID.String
		ws.TaskID = &t
	}
	if worktreePath.Valid {
		w := worktreePath.String
		ws.WorktreePath = &w
	}
	if runtimeSessionID.Valid {
		r := runtimeSessionID.String
		ws.RuntimeSessionID = &r
	}
	if previewURL.Valid {
		p := previewURL.String
		ws.PreviewURL = &p
	}
	if settings.Valid {
		ws.Settings = json.RawMessage(settings.String)
	}
	if deletedAt.Valid {
		ws.DeletedAt = &deletedAt.Time
	}

	return &ws, nil
}

// getRepoOwnerName extracts owner and name from repository record.
func (f *Factory) getRepoOwnerName(ctx context.Context, repoID string) (owner, name string, err error) {
	var fullName string
	err = f.db.QueryRowContext(ctx, `
		SELECT full_name FROM repositories WHERE id = $1
	`, repoID).Scan(&fullName)
	if err != nil {
		return "", "", fmt.Errorf("get repository: %w", err)
	}

	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repository full_name: %s", fullName)
	}
	return parts[0], parts[1], nil
}
