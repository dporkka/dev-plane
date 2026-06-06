// Package reviewer performs code review on agent-generated changes.
//
// The Reviewer analyzes git diffs, test results, and agent step history to produce
// a structured review report.
package reviewer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ai-dev-control-plane/models"
)

// Reviewer performs code review on agent-generated changes.
type Reviewer struct {
	db     *sql.DB
	logger *slog.Logger
}

// ReviewReport contains the complete review.
type ReviewReport struct {
	RunID         string      `json:"run_id"`
	Summary       string      `json:"summary"`
	Findings      []Finding   `json:"findings"`
	RiskLevel     string      `json:"risk_level"` // low, medium, high, critical
	Approvable    bool        `json:"approvable"`
	Suggestions   []string    `json:"suggestions"`
	TestCoverage  string      `json:"test_coverage"`
	SecurityNotes string      `json:"security_notes"`
	DiffSummary   DiffSummary `json:"diff_summary"`
	CreatedAt     time.Time   `json:"created_at"`
}

// Finding represents a single review finding.
type Finding struct {
	Severity   string `json:"severity"` // critical, high, medium, low, info
	File       string `json:"file"`
	Line       int    `json:"line"`
	Message    string `json:"message"`
	Category   string `json:"category"` // correctness, security, performance, style, testing
	Suggestion string `json:"suggestion,omitempty"`
}

// DiffSummary contains statistics about the changes.
type DiffSummary struct {
	FilesChanged int          `json:"files_changed"`
	Insertions   int          `json:"insertions"`
	Deletions    int          `json:"deletions"`
	Files        []FileChange `json:"files"`
}

// FileChange represents a single file's changes.
type FileChange struct {
	Path        string `json:"path"`
	Status      string `json:"status"` // added, modified, deleted
	Insertions  int    `json:"insertions"`
	Deletions   int    `json:"deletions"`
	IsTest      bool   `json:"is_test"`
	IsConfig    bool   `json:"is_config"`
	IsMigration bool   `json:"is_migration"`
}

// TestResults holds test/lint/typecheck results for review.
type TestResults struct {
	TestsPassed     bool     `json:"tests_passed"`
	TestsTotal      int      `json:"tests_total"`
	TestsFailed     int      `json:"tests_failed"`
	LintPassed      bool     `json:"lint_passed"`
	LintErrors      []string `json:"lint_errors,omitempty"`
	TypecheckPassed bool     `json:"typecheck_passed"`
	TypecheckErrors []string `json:"typecheck_errors,omitempty"`
	CoveragePercent float64  `json:"coverage_percent"`
}

// NewReviewer creates a code reviewer.
func NewReviewer(db *sql.DB, logger *slog.Logger) *Reviewer {
	return &Reviewer{
		db:     db,
		logger: logger,
	}
}

// Review analyzes changes in a workspace and produces a review report.
func (r *Reviewer) Review(ctx context.Context, runID string) (*ReviewReport, error) {
	r.logger.Info("starting review", "run_id", runID)

	// 1. Get agent run details
	var run models.AgentRun
	err := r.db.QueryRowContext(ctx, `
		SELECT id, task_id, workspace_id, agent_role, model, provider, status,
		       prompt_tokens, completion_tokens, total_cost, summary, metadata, created_at, updated_at
		FROM agent_runs WHERE id = $1
	`, runID).Scan(
		&run.ID, &run.TaskID, &run.WorkspaceID, &run.AgentRole, &run.Model, &run.Provider,
		&run.Status, &run.PromptTokens, &run.CompletionTokens, &run.TotalCost,
		&run.Summary, &run.Metadata, &run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get agent run %s: %w", runID, err)
	}

	if run.WorkspaceID == nil {
		return nil, fmt.Errorf("agent run %s has no workspace", runID)
	}

	// 2. Get git diff from workspace
	diff, err := r.getGitDiff(ctx, *run.WorkspaceID)
	if err != nil {
		r.logger.Warn("failed to get git diff, proceeding with empty diff", "error", err)
		diff = ""
	}

	// 3. Get agent run steps
	steps, err := r.getAgentSteps(ctx, runID)
	if err != nil {
		r.logger.Warn("failed to get agent steps, proceeding without", "error", err)
		steps = nil
	}

	report := r.generateReview(diff, nil, steps)
	report.RunID = runID

	// 5. Save review report
	if err := r.Save(ctx, report); err != nil {
		return nil, fmt.Errorf("save review report: %w", err)
	}

	r.logger.Info("review completed",
		"run_id", runID,
		"risk_level", report.RiskLevel,
		"approvable", report.Approvable,
		"findings", len(report.Findings),
	)

	return report, nil
}

// getGitDiff retrieves the git diff from a workspace.
func (r *Reviewer) getGitDiff(ctx context.Context, workspaceID string) (string, error) {
	var worktreePath string
	err := r.db.QueryRowContext(ctx, `
		SELECT worktree_path FROM workspaces WHERE id = $1
	`, workspaceID).Scan(&worktreePath)
	if err != nil {
		return "", fmt.Errorf("get workspace path: %w", err)
	}
	if worktreePath == "" {
		return "", fmt.Errorf("workspace %s has no worktree path", workspaceID)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "diff", "--no-ext-diff", "--binary", "HEAD", "--")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff workspace %s: %w: %s", workspaceID, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// getAgentSteps retrieves the steps for an agent run.
func (r *Reviewer) getAgentSteps(ctx context.Context, runID string) ([]models.AgentStep, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, agent_run_id, step_number, step_type, status, content,
		       tool_name, tool_input, tool_output, command, command_output,
		       exit_code, file_path, diff, cost, latency_ms, created_at
		FROM agent_steps WHERE agent_run_id = $1 ORDER BY step_number ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []models.AgentStep
	for rows.Next() {
		var s models.AgentStep
		var content, toolName, toolInput, toolOutput, command, commandOutput, filePath, diff sql.NullString
		var exitCode sql.NullInt32
		err := rows.Scan(
			&s.ID, &s.AgentRunID, &s.StepNumber, &s.StepType, &s.Status,
			&content, &toolName, &toolInput, &toolOutput, &command, &commandOutput,
			&exitCode, &filePath, &diff, &s.Cost, &s.LatencyMs, &s.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		if content.Valid {
			s.Content = &content.String
		}
		if toolName.Valid {
			s.ToolName = &toolName.String
		}
		if toolInput.Valid {
			s.ToolInput = json.RawMessage(toolInput.String)
		}
		if toolOutput.Valid {
			s.ToolOutput = json.RawMessage(toolOutput.String)
		}
		if command.Valid {
			s.Command = &command.String
		}
		if commandOutput.Valid {
			s.CommandOutput = &commandOutput.String
		}
		if exitCode.Valid {
			ec := int(exitCode.Int32)
			s.ExitCode = &ec
		}
		if filePath.Valid {
			s.FilePath = &filePath.String
		}
		if diff.Valid {
			s.Diff = &diff.String
		}
		steps = append(steps, s)
	}
	return steps, rows.Err()
}

// generateReview creates a deterministic review from diff stats and test results.
func (r *Reviewer) generateReview(diff string, testResults *TestResults, steps []models.AgentStep) *ReviewReport {
	report := &ReviewReport{
		RunID:       uuid.New().String(),
		RiskLevel:   "low",
		Approvable:  true,
		Suggestions: []string{},
		Findings:    []Finding{},
		CreatedAt:   time.Now().UTC(),
	}

	// Parse diff statistics
	diffSummary := parseDiffStats(diff)
	report.DiffSummary = diffSummary

	// Generate summary
	report.Summary = fmt.Sprintf(
		"Automated review of %d files changed (%d insertions, %d deletions).",
		diffSummary.FilesChanged, diffSummary.Insertions, diffSummary.Deletions,
	)

	// Assess risk based on change size
	riskScore := 0
	if diffSummary.FilesChanged > 20 {
		riskScore += 2
		report.Findings = append(report.Findings, Finding{
			Severity:   "medium",
			File:       "*",
			Line:       0,
			Message:    fmt.Sprintf("Large change set: %d files modified", diffSummary.FilesChanged),
			Category:   "correctness",
			Suggestion: "Consider breaking this into smaller, focused changes.",
		})
	}
	if diffSummary.Insertions+diffSummary.Deletions > 500 {
		riskScore += 2
		report.Findings = append(report.Findings, Finding{
			Severity:   "medium",
			File:       "*",
			Line:       0,
			Message:    fmt.Sprintf("Large diff: %d lines changed", diffSummary.Insertions+diffSummary.Deletions),
			Category:   "correctness",
			Suggestion: "Large diffs are harder to review. Consider splitting into multiple PRs.",
		})
	}

	// Check for test files
	hasTests := false
	for _, f := range diffSummary.Files {
		if f.IsTest {
			hasTests = true
		}
		if f.IsMigration {
			riskScore += 2
			report.Findings = append(report.Findings, Finding{
				Severity:   "high",
				File:       f.Path,
				Line:       0,
				Message:    "Database migration detected",
				Category:   "correctness",
				Suggestion: "Verify migration is reversible and has been tested on a copy of production data.",
			})
		}
		if f.IsConfig {
			report.Findings = append(report.Findings, Finding{
				Severity:   "info",
				File:       f.Path,
				Line:       0,
				Message:    "Configuration file changed",
				Category:   "correctness",
				Suggestion: "Verify configuration changes are intentional and documented.",
			})
		}
	}

	// Test coverage assessment
	if hasTests {
		report.TestCoverage = "Tests included"
		if testResults != nil {
			report.TestCoverage = fmt.Sprintf("%.1f%% coverage", testResults.CoveragePercent)
		}
	} else {
		riskScore += 1
		report.TestCoverage = "No tests detected"
		report.Findings = append(report.Findings, Finding{
			Severity:   "medium",
			File:       "*",
			Line:       0,
			Message:    "No test files detected in this change",
			Category:   "testing",
			Suggestion: "Consider adding unit tests for new or modified code.",
		})
	}

	// Check test results if available
	if testResults != nil {
		if !testResults.TestsPassed {
			riskScore += 3
			report.Approvable = false
			report.Findings = append(report.Findings, Finding{
				Severity:   "critical",
				File:       "*",
				Line:       0,
				Message:    fmt.Sprintf("Tests failing: %d/%d failed", testResults.TestsFailed, testResults.TestsTotal),
				Category:   "testing",
				Suggestion: "Fix failing tests before merging.",
			})
		}
		if !testResults.LintPassed {
			riskScore += 1
			report.Findings = append(report.Findings, Finding{
				Severity:   "low",
				File:       "*",
				Line:       0,
				Message:    "Lint errors detected",
				Category:   "style",
				Suggestion: "Fix lint errors to maintain code quality.",
			})
		}
		if !testResults.TypecheckPassed {
			riskScore += 2
			report.Approvable = false
			report.Findings = append(report.Findings, Finding{
				Severity:   "high",
				File:       "*",
				Line:       0,
				Message:    "Type check errors detected",
				Category:   "correctness",
				Suggestion: "Fix type errors before merging.",
			})
		}
	}

	// Security checks
	report.SecurityNotes = r.checkSecurity(diffSummary)

	// Determine risk level
	switch {
	case riskScore >= 5:
		report.RiskLevel = "critical"
		report.Approvable = false
	case riskScore >= 3:
		report.RiskLevel = "high"
		report.Approvable = false
	case riskScore >= 1:
		report.RiskLevel = "medium"
	default:
		report.RiskLevel = "low"
	}

	// Add generic suggestions
	if len(report.Suggestions) == 0 {
		report.Suggestions = []string{
			"Review all modified files for correctness.",
			"Ensure error handling is adequate.",
			"Verify no sensitive data is exposed in the diff.",
		}
	}

	return report
}

// checkSecurity performs basic security checks on the diff.
func (r *Reviewer) checkSecurity(summary DiffSummary) string {
	var notes []string
	for _, f := range summary.Files {
		lower := strings.ToLower(f.Path)
		if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "key") {
			notes = append(notes, fmt.Sprintf("File '%s' may contain sensitive data - verify no secrets are exposed", f.Path))
		}
		if strings.HasSuffix(lower, ".env") || strings.HasSuffix(lower, ".env.local") || strings.HasSuffix(lower, ".env.production") {
			notes = append(notes, fmt.Sprintf("Environment file '%s' changed - verify no secrets committed", f.Path))
		}
	}
	if len(notes) == 0 {
		return "No obvious security concerns detected in changed files."
	}
	return strings.Join(notes, "; ")
}

// parseDiffStats extracts file change statistics from git diff.
func parseDiffStats(diff string) DiffSummary {
	summary := DiffSummary{
		Files: []FileChange{},
	}
	if diff == "" {
		return summary
	}

	// Parse diff --git lines to find files
	gitDiffRe := regexp.MustCompile(`diff --git a/(\S+) b/(\S+)`)
	newFileRe := regexp.MustCompile(`--- /dev/null`)
	delFileRe := regexp.MustCompile(`\+\+\+ /dev/null`)

	lines := strings.Split(diff, "\n")
	var currentFile *FileChange

	for i, line := range lines {
		// Match new file headers
		if matches := gitDiffRe.FindStringSubmatch(line); len(matches) >= 3 {
			if currentFile != nil {
				summary.Files = append(summary.Files, *currentFile)
			}
			path := matches[2] // b/ path is the new one
			isTest, isConfig, isMigration := classifyFile(path)
			currentFile = &FileChange{
				Path:        path,
				Status:      "modified",
				IsTest:      isTest,
				IsConfig:    isConfig,
				IsMigration: isMigration,
			}
			summary.FilesChanged++
			continue
		}

		// Check for new/deleted file markers in subsequent lines
		if currentFile != nil {
			if newFileRe.MatchString(line) {
				currentFile.Status = "added"
			}
			if delFileRe.MatchString(line) {
				currentFile.Status = "deleted"
			}
		}

		// Count insertions/deletions
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			summary.Insertions++
			if currentFile != nil {
				currentFile.Insertions++
			}
		}
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			summary.Deletions++
			if currentFile != nil {
				currentFile.Deletions++
			}
		}

		// Parse @@ lines for line numbers
		if strings.HasPrefix(line, "@@") && currentFile != nil {
			lineNum := extractLineNumber(line)
			if lineNum > 0 && currentFile.Insertions == 0 && currentFile.Deletions == 0 {
				// Just capturing the starting line
				_ = lineNum
			}
		}

		// Last line
		if i == len(lines)-1 && currentFile != nil {
			summary.Files = append(summary.Files, *currentFile)
		}
	}

	return summary
}

// extractLineNumber extracts the line number from a diff hunk header.
func extractLineNumber(hunk string) int {
	re := regexp.MustCompile(`@@ -\d+(?:,\d+)? \+(\d+)`)
	matches := re.FindStringSubmatch(hunk)
	if len(matches) >= 2 {
		n, _ := strconv.Atoi(matches[1])
		return n
	}
	return 0
}

// classifyFile categorizes a file change.
func classifyFile(path string) (isTest, isConfig, isMigration bool) {
	lower := strings.ToLower(path)

	// Test files
	if strings.Contains(lower, "_test.") || strings.Contains(lower, ".test.") ||
		strings.HasSuffix(lower, "_tests.go") || strings.HasPrefix(lower, "test/") ||
		strings.HasPrefix(lower, "tests/") {
		isTest = true
	}

	// Config files
	if strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".toml") ||
		strings.HasSuffix(lower, ".ini") || strings.HasSuffix(lower, ".env") ||
		strings.HasSuffix(lower, ".config.js") || strings.HasSuffix(lower, ".config.ts") ||
		strings.Contains(lower, "dockerfile") || strings.Contains(lower, "docker-compose") ||
		strings.Contains(lower, ".github/") || strings.Contains(lower, ".gitlab-") {
		isConfig = true
	}

	// Migration files
	if strings.Contains(lower, "migration") || strings.Contains(lower, "migrat") ||
		strings.Contains(lower, "/migrations/") || strings.Contains(lower, "/migrate/") ||
		regexp.MustCompile(`^\d+_.*\.sql$`).MatchString(path) ||
		regexp.MustCompile(`V\d+__.*\.sql$`).MatchString(path) {
		isMigration = true
	}

	return
}

// Save persists the review report to the database.
func (r *Reviewer) Save(ctx context.Context, report *ReviewReport) error {
	findingsJSON, err := json.Marshal(report.Findings)
	if err != nil {
		return fmt.Errorf("marshal findings: %w", err)
	}
	suggestionsJSON, err := json.Marshal(report.Suggestions)
	if err != nil {
		return fmt.Errorf("marshal suggestions: %w", err)
	}
	diffSummaryJSON, err := json.Marshal(report.DiffSummary)
	if err != nil {
		return fmt.Errorf("marshal diff summary: %w", err)
	}

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO review_reports (
			id, run_id, summary, findings, risk_level, approvable,
			suggestions, test_coverage, security_notes, diff_summary, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (run_id) DO UPDATE SET
			summary = EXCLUDED.summary,
			findings = EXCLUDED.findings,
			risk_level = EXCLUDED.risk_level,
			approvable = EXCLUDED.approvable,
			suggestions = EXCLUDED.suggestions,
			test_coverage = EXCLUDED.test_coverage,
			security_notes = EXCLUDED.security_notes,
			diff_summary = EXCLUDED.diff_summary,
			created_at = EXCLUDED.created_at
	`, uuid.New().String(), report.RunID, report.Summary, findingsJSON,
		report.RiskLevel, report.Approvable, suggestionsJSON,
		report.TestCoverage, report.SecurityNotes, diffSummaryJSON, report.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert review report: %w", err)
	}
	return nil
}

// Get retrieves a review report by run ID.
func (r *Reviewer) Get(ctx context.Context, runID string) (*ReviewReport, error) {
	var report ReviewReport
	var findingsJSON, suggestionsJSON, diffSummaryJSON sql.NullString

	err := r.db.QueryRowContext(ctx, `
		SELECT id, run_id, summary, findings, risk_level, approvable,
		       suggestions, test_coverage, security_notes, diff_summary, created_at
		FROM review_reports WHERE run_id = $1
	`, runID).Scan(
		&report.RunID, &report.RunID, &report.Summary, &findingsJSON,
		&report.RiskLevel, &report.Approvable, &suggestionsJSON,
		&report.TestCoverage, &report.SecurityNotes, &diffSummaryJSON,
		&report.CreatedAt,
	)
	// We use run_id for both id and run_id since id is auto-generated
	// Re-scan properly
	if err != nil {
		// Re-query with proper column mapping
		var id string
		err = r.db.QueryRowContext(ctx, `
			SELECT id, run_id, summary, findings, risk_level, approvable,
			       suggestions, test_coverage, security_notes, diff_summary, created_at
			FROM review_reports WHERE run_id = $1
		`, runID).Scan(
			&id, &report.RunID, &report.Summary, &findingsJSON,
			&report.RiskLevel, &report.Approvable, &suggestionsJSON,
			&report.TestCoverage, &report.SecurityNotes, &diffSummaryJSON,
			&report.CreatedAt,
		)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("review report not found for run %s", runID)
			}
			return nil, fmt.Errorf("get review report: %w", err)
		}
	}

	if findingsJSON.Valid {
		json.Unmarshal([]byte(findingsJSON.String), &report.Findings)
	}
	if suggestionsJSON.Valid {
		json.Unmarshal([]byte(suggestionsJSON.String), &report.Suggestions)
	}
	if diffSummaryJSON.Valid {
		json.Unmarshal([]byte(diffSummaryJSON.String), &report.DiffSummary)
	}

	return &report, nil
}
