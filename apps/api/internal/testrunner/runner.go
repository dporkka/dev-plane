// Package testrunner executes lint, typecheck, and tests in a workspace.
// It provides a unified interface for quality checks that run after
// agent code modifications.
package testrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Runner executes lint, typecheck, and tests in a workspace.
type Runner struct {
	logger *slog.Logger
}

// NewRunner creates a test runner.
func NewRunner(logger *slog.Logger) *Runner {
	return &Runner{logger: logger}
}

// TestResult contains the outcome of a single check.
type TestResult struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Passed     bool   `json:"passed"`
	DurationMs int    `json:"duration_ms"`
}

// AllResults aggregates all check results.
type AllResults struct {
	Lint      *TestResult `json:"lint,omitempty"`
	Typecheck *TestResult `json:"typecheck,omitempty"`
	Tests     *TestResult `json:"tests,omitempty"`
	Overall   bool        `json:"overall_passed"`
}

// ProjectConfig holds project-specific configuration for test discovery.
// This is a placeholder type that will be expanded when the models package
// defines a full ProjectConfig.
type ProjectConfig struct {
	Language     string            `json:"language"`
	TestCommand  string            `json:"test_command,omitempty"`
	LintCommand  string            `json:"lint_command,omitempty"`
	TypecheckCmd string            `json:"typecheck_command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	TimeoutSec   int               `json:"timeout_sec,omitempty"`
}

// RunAll runs all checks (lint, typecheck, tests) and returns results.
func (r *Runner) RunAll(ctx context.Context, workspacePath string, config *ProjectConfig) (*AllResults, error) {
	r.logger.Info("running all checks", "workspace", workspacePath)

	results := &AllResults{
		Overall: true,
	}

	// Run lint
	lintResult, err := r.RunLint(ctx, workspacePath, config)
	if err != nil {
		r.logger.Warn("lint check error", "error", err)
	}
	results.Lint = lintResult
	if lintResult != nil && !lintResult.Passed {
		results.Overall = false
	}

	// Run typecheck
	typeResult, err := r.RunTypecheck(ctx, workspacePath, config)
	if err != nil {
		r.logger.Warn("typecheck error", "error", err)
	}
	results.Typecheck = typeResult
	if typeResult != nil && !typeResult.Passed {
		results.Overall = false
	}

	// Run tests
	testResult, err := r.RunTests(ctx, workspacePath, config)
	if err != nil {
		r.logger.Warn("test run error", "error", err)
	}
	results.Tests = testResult
	if testResult != nil && !testResult.Passed {
		results.Overall = false
	}

	r.logger.Info("all checks complete",
		"overall_passed", results.Overall,
		"lint_passed", lintResult != nil && lintResult.Passed,
		"typecheck_passed", typeResult != nil && typeResult.Passed,
		"tests_passed", testResult != nil && testResult.Passed,
	)

	return results, nil
}

// RunLint executes the lint command.
func (r *Runner) RunLint(ctx context.Context, workspacePath string, config *ProjectConfig) (*TestResult, error) {
	command, timeout := r.findLintCommand(workspacePath, config)
	if command == "" {
		r.logger.Debug("no lint command found, skipping")
		return nil, nil
	}

	r.logger.Info("running lint", "command", command)
	return r.execute(ctx, workspacePath, command, timeout, config), nil
}

// RunTypecheck executes the typecheck command.
func (r *Runner) RunTypecheck(ctx context.Context, workspacePath string, config *ProjectConfig) (*TestResult, error) {
	command, timeout := r.findTypecheckCommand(workspacePath, config)
	if command == "" {
		r.logger.Debug("no typecheck command found, skipping")
		return nil, nil
	}

	r.logger.Info("running typecheck", "command", command)
	return r.execute(ctx, workspacePath, command, timeout, config), nil
}

// RunTests executes the test command.
func (r *Runner) RunTests(ctx context.Context, workspacePath string, config *ProjectConfig) (*TestResult, error) {
	command, timeout := r.findTestCommand(workspacePath, config)
	if command == "" {
		r.logger.Debug("no test command found, skipping")
		return nil, nil
	}

	r.logger.Info("running tests", "command", command)
	return r.execute(ctx, workspacePath, command, timeout, config), nil
}

// execute runs a command with timeout and captures output.
func (r *Runner) execute(ctx context.Context, workspacePath, command string, timeoutSec int, config *ProjectConfig) *TestResult {
	if timeoutSec <= 0 {
		timeoutSec = 300 // Default 5 minutes
	}
	if timeoutSec > 600 {
		timeoutSec = 600 // Max 10 minutes
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	start := time.Now()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = workspacePath

	// Set environment variables from config
	if config != nil && config.Env != nil {
		cmd.Env = os.Environ()
		for k, v := range config.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	output, err := cmd.CombinedOutput()
	durationMs := int(time.Since(start).Milliseconds())

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	// Separate stdout and stderr is not available with CombinedOutput,
	// so we treat the combined output as stdout for simplicity.
	outputStr := truncateOutput(string(output), 50000)

	// Check for context timeout
	if ctx.Err() == context.DeadlineExceeded {
		outputStr += "\n[TIMEOUT: command exceeded time limit]"
		exitCode = -2
	}

	return &TestResult{
		Command:    command,
		ExitCode:   exitCode,
		Stdout:     outputStr,
		Stderr:     "",
		Passed:     exitCode == 0,
		DurationMs: durationMs,
	}
}

// ---------------------------------------------------------------------------
// Command discovery
// ---------------------------------------------------------------------------

// findTestCommand determines the test command for a project.
func (r *Runner) findTestCommand(workspacePath string, config *ProjectConfig) (string, int) {
	timeout := 300
	if config != nil && config.TimeoutSec > 0 {
		timeout = config.TimeoutSec
	}

	// Check config override first
	if config != nil && config.TestCommand != "" {
		return config.TestCommand, timeout
	}

	// Auto-detect based on project files
	if fileExists(workspacePath, "go.mod") {
		return "go test ./...", timeout
	}
	if fileExists(workspacePath, "package.json") {
		if fileExists(workspacePath, "jest.config.js") || fileExists(workspacePath, "jest.config.ts") {
			return "npx jest", timeout
		}
		return "npm test", timeout
	}
	if fileExists(workspacePath, "Cargo.toml") {
		return "cargo test", timeout
	}
	if fileExists(workspacePath, "pom.xml") {
		return "mvn test", timeout
	}
	if fileExists(workspacePath, "build.gradle") || fileExists(workspacePath, "build.gradle.kts") {
		return "./gradlew test", timeout
	}
	if fileExists(workspacePath, "requirements.txt") || fileExists(workspacePath, "pyproject.toml") {
		return "python -m pytest", timeout
	}
	if fileExists(workspacePath, "Gemfile") {
		return "bundle exec rspec", timeout
	}
	if fileExists(workspacePath, "composer.json") {
		return "vendor/bin/phpunit", timeout
	}

	return "", 0
}

// findLintCommand determines the lint command for a project.
func (r *Runner) findLintCommand(workspacePath string, config *ProjectConfig) (string, int) {
	timeout := 120
	if config != nil && config.TimeoutSec > 0 {
		timeout = config.TimeoutSec
	}

	// Config override
	if config != nil && config.LintCommand != "" {
		return config.LintCommand, timeout
	}

	// Auto-detect
	if fileExists(workspacePath, "go.mod") {
		return "go vet ./...", timeout
	}
	if fileExists(workspacePath, "package.json") {
		// Check for ESLint
		if fileExists(workspacePath, ".eslintrc.js") || fileExists(workspacePath, ".eslintrc.json") || fileExists(workspacePath, ".eslintrc") {
			return "npx eslint .", timeout
		}
		// Check for Prettier as a basic check
		if fileExists(workspacePath, ".prettierrc") || fileExists(workspacePath, ".prettierrc.json") {
			return "npx prettier --check .", timeout
		}
	}
	if fileExists(workspacePath, "Cargo.toml") {
		return "cargo clippy", timeout
	}
	if fileExists(workspacePath, "pom.xml") || fileExists(workspacePath, "build.gradle") {
		return "", 0 // Java lint typically via compiler warnings
	}
	if fileExists(workspacePath, "pyproject.toml") || fileExists(workspacePath, "setup.cfg") {
		if hasPyprojectTool(workspacePath, "ruff") {
			return "ruff check .", timeout
		}
		if hasPyprojectTool(workspacePath, "flake8") {
			return "flake8 .", timeout
		}
		if hasPyprojectTool(workspacePath, "pylint") {
			return "pylint .", timeout
		}
	}
	if fileExists(workspacePath, "requirements.txt") {
		return "flake8 .", timeout
	}

	return "", 0
}

// findTypecheckCommand determines the typecheck command for a project.
func (r *Runner) findTypecheckCommand(workspacePath string, config *ProjectConfig) (string, int) {
	timeout := 120
	if config != nil && config.TimeoutSec > 0 {
		timeout = config.TimeoutSec
	}

	// Config override
	if config != nil && config.TypecheckCmd != "" {
		return config.TypecheckCmd, timeout
	}

	// Auto-detect
	if fileExists(workspacePath, "go.mod") {
		return "go build ./...", timeout // go build does type checking
	}
	if fileExists(workspacePath, "tsconfig.json") {
		return "npx tsc --noEmit", timeout
	}
	if fileExists(workspacePath, "Cargo.toml") {
		return "cargo check", timeout
	}
	if fileExists(workspacePath, "pom.xml") {
		return "mvn compile", timeout
	}
	if fileExists(workspacePath, "build.gradle") || fileExists(workspacePath, "build.gradle.kts") {
		return "./gradlew compileJava", timeout
	}
	if fileExists(workspacePath, "pyproject.toml") || fileExists(workspacePath, "requirements.txt") {
		// Python: mypy if available
		if hasPyprojectTool(workspacePath, "mypy") {
			return "mypy .", timeout
		}
	}

	return "", 0
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fileExists checks if a file exists in the workspace.
func fileExists(workspacePath, name string) bool {
	_, err := os.Stat(filepath.Join(workspacePath, name))
	return err == nil
}

// hasPyprojectTool checks if pyproject.toml has a specific tool configured.
func hasPyprojectTool(workspacePath, tool string) bool {
	data, err := os.ReadFile(filepath.Join(workspacePath, "pyproject.toml"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), fmt.Sprintf("[%s", tool)) ||
		strings.Contains(string(data), fmt.Sprintf("[tool.%s", tool))
}

// truncateOutput truncates output to maxLen characters.
func truncateOutput(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [output truncated]"
}

// ParseTestOutput parses test command output and returns structured results.
func ParseTestOutput(output string, exitCode int) (passed bool, total, failed, skipped int) {
	passed = exitCode == 0

	// Try to parse Go test output
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)

		// Go test: "ok  	package	0.5s"
		if strings.HasPrefix(line, "ok ") {
			total++
		}
		// Go test: "FAIL	package	0.5s"
		if strings.HasPrefix(line, "FAIL") && strings.Contains(line, "\t") {
			total++
			failed++
		}
		// Go test: "?  	package	[no test files]"
		if strings.HasPrefix(line, "? ") {
			skipped++
		}
		// Go test summary line
		// "50 passed, 2 failed, 3 skipped"
		if strings.Contains(line, "passed") || strings.Contains(line, "failed") {
			// Try to extract numbers
			fmt.Sscanf(line, "%d passed, %d failed, %d skipped", &total, &failed, &skipped)
		}
	}

	return passed, total, failed, skipped
}

// ResultSummary creates a human-readable summary of all results.
func (r *AllResults) ResultSummary() string {
	var parts []string

	if r.Lint != nil {
		status := "PASS"
		if !r.Lint.Passed {
			status = "FAIL"
		}
		parts = append(parts, fmt.Sprintf("Lint: %s (%s, %dms)", status, r.Lint.Command, r.Lint.DurationMs))
	}
	if r.Typecheck != nil {
		status := "PASS"
		if !r.Typecheck.Passed {
			status = "FAIL"
		}
		parts = append(parts, fmt.Sprintf("Typecheck: %s (%s, %dms)", status, r.Typecheck.Command, r.Typecheck.DurationMs))
	}
	if r.Tests != nil {
		status := "PASS"
		if !r.Tests.Passed {
			status = "FAIL"
		}
		parts = append(parts, fmt.Sprintf("Tests: %s (%s, %dms)", status, r.Tests.Command, r.Tests.DurationMs))
	}

	overall := "PASS"
	if !r.Overall {
		overall = "FAIL"
	}

	if len(parts) == 0 {
		return fmt.Sprintf("Overall: %s (no checks ran)", overall)
	}
	return fmt.Sprintf("Overall: %s | %s", overall, strings.Join(parts, " | "))
}

// ToJSON serializes the results to JSON.
func (r *AllResults) ToJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
