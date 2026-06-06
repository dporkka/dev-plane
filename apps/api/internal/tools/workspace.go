// Package tools provides real implementations of agent tools that operate
// on workspace files, execute commands, run tests, and interact with git.
package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// WorkspaceTools provides real implementations of agent tools.
type WorkspaceTools struct {
	logger *slog.Logger
}

// NewWorkspaceTools creates tool implementations backed by a logger.
// The db parameter is reserved for future use (query caching, etc.)
func NewWorkspaceTools(logger *slog.Logger) *WorkspaceTools {
	return &WorkspaceTools{
		logger: logger,
	}
}

// ReadFile reads a file from the workspace.
// Input: {"path": "src/main.go"}
// Output: {"content": "...", "size": 1234, "lines": 45}
func (t *WorkspaceTools) ReadFile(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	start := time.Now()

	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for read_file: %w", err)
	}

	filePath, err := resolveWorkspacePath(workspacePath, req.Path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return json.Marshal(map[string]any{
				"error": fmt.Sprintf("file not found: %s", req.Path),
			})
		}
		return nil, fmt.Errorf("read file %s: %w", req.Path, err)
	}

	content := string(data)
	lines := strings.Count(content, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		lines++
	}

	t.logger.Debug("read_file", "path", req.Path, "size", len(data), "lines", lines, "latency_ms", time.Since(start).Milliseconds())

	return json.Marshal(map[string]any{
		"content": content,
		"size":    len(data),
		"lines":   lines,
	})
}

// WriteFile writes content to a file in the workspace.
// Input: {"path": "src/main.go", "content": "..."}
// Output: {"success": true, "bytes_written": 1234}
// SECURITY: Validates path is within workspace (prevents traversal).
func (t *WorkspaceTools) WriteFile(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for write_file: %w", err)
	}

	filePath, err := resolveWorkspacePath(workspacePath, req.Path)
	if err != nil {
		return nil, err
	}

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", dir, err)
	}

	bytesWritten := len(req.Content)
	if err := os.WriteFile(filePath, []byte(req.Content), 0644); err != nil {
		return nil, fmt.Errorf("write file %s: %w", req.Path, err)
	}

	t.logger.Debug("write_file", "path", req.Path, "bytes_written", bytesWritten)

	return json.Marshal(map[string]any{
		"success":       true,
		"bytes_written": bytesWritten,
	})
}

// SearchFiles searches files using ripgrep (rg) or grep fallback.
// Input: {"query": "func main", "path": ".", "glob": "*.go"}
// Output: {"results": [{"file": "...", "line": 10, "content": "..."}]}
func (t *WorkspaceTools) SearchFiles(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Query string `json:"query"`
		Path  string `json:"path,omitempty"`
		Glob  string `json:"glob,omitempty"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for search_files: %w", err)
	}

	searchPath, err := resolveWorkspacePath(workspacePath, req.Path)
	if err != nil {
		return nil, err
	}

	// Check for binary pattern that could be dangerous
	if isDangerousQuery(req.Query) {
		return nil, fmt.Errorf("dangerous search query rejected: %s", req.Query)
	}

	var cmd *exec.Cmd
	if hasRipgrep() {
		args := []string{"--line-number", "--no-heading", "--with-filename", "--color=never"}
		if req.Glob != "" {
			args = append(args, "--glob", req.Glob)
		}
		// Add fixed-strings for simple queries to avoid regex injection
		if isLiteralQuery(req.Query) {
			args = append(args, "--fixed-strings")
		}
		args = append(args, req.Query, searchPath)
		cmd = exec.CommandContext(ctx, "rg", args...)
	} else {
		// Fallback to grep
		args := []string{"-r", "-n", "-H", "--color=never"}
		if req.Glob != "" {
			// grep doesn't support glob directly; use include
			args = append(args, "--include", req.Glob)
		}
		args = append(args, req.Query, searchPath)
		cmd = exec.CommandContext(ctx, "grep", args...)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		// No matches is not an error
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return json.Marshal(map[string]any{
				"results": []any{},
				"count":   0,
			})
		}
		return nil, fmt.Errorf("search files: %w (output: %s)", err, string(output))
	}

	type searchResult struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Content string `json:"content"`
	}

	var results []searchResult
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 3)
		if len(parts) >= 3 {
			relPath, _ := filepath.Rel(workspacePath, parts[0])
			if relPath == "" {
				relPath = parts[0]
			}
			var lineNum int
			fmt.Sscanf(parts[1], "%d", &lineNum)
			results = append(results, searchResult{
				File:    relPath,
				Line:    lineNum,
				Content: parts[2],
			})
		}
	}

	return json.Marshal(map[string]any{
		"results": results,
		"count":   len(results),
	})
}

// ListDirectory lists files and directories.
// Input: {"path": "."}
// Output: {"entries": [{"name": "src", "type": "directory"}, {"name": "main.go", "type": "file", "size": 1234}]}
func (t *WorkspaceTools) ListDirectory(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Path string `json:"path,omitempty"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for list_directory: %w", err)
	}
	if req.Path == "" {
		req.Path = "."
	}

	dirPath, err := resolveWorkspacePath(workspacePath, req.Path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("list directory %s: %w", req.Path, err)
	}

	type dirEntry struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Size int64  `json:"size,omitempty"`
	}

	var resultEntries []dirEntry
	for _, entry := range entries {
		e := dirEntry{
			Name: entry.Name(),
			Type: "file",
		}
		if entry.IsDir() {
			e.Type = "directory"
		} else if info, err := entry.Info(); err == nil {
			e.Size = info.Size()
		}
		resultEntries = append(resultEntries, e)
	}

	return json.Marshal(map[string]any{
		"entries": resultEntries,
		"path":    req.Path,
		"count":   len(resultEntries),
	})
}

// ApplyPatch applies a unified diff patch using 'git apply'.
// Input: {"patch": "--- a/file.go\\n+++ b/file.go\\n..."}
// Output: {"success": true, "files_modified": ["file.go"]}
func (t *WorkspaceTools) ApplyPatch(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for apply_patch: %w", err)
	}

	// Validate patch is non-empty
	if strings.TrimSpace(req.Patch) == "" {
		return nil, fmt.Errorf("patch is empty")
	}

	// Write patch to temporary file
	patchFile := filepath.Join(workspacePath, ".tmp_patch_"+fmt.Sprintf("%d", time.Now().UnixNano()))
	if err := os.WriteFile(patchFile, []byte(req.Patch), 0644); err != nil {
		return nil, fmt.Errorf("write patch file: %w", err)
	}
	defer os.Remove(patchFile)

	// Try git apply first, then fall back to patch command
	cmd := exec.CommandContext(ctx, "git", "-C", workspacePath, "apply", "--check", patchFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		return json.Marshal(map[string]any{
			"success": false,
			"error":   fmt.Sprintf("patch does not apply cleanly: %s", string(output)),
		})
	}

	// Apply the patch
	cmd = exec.CommandContext(ctx, "git", "-C", workspacePath, "apply", patchFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return json.Marshal(map[string]any{
			"success": false,
			"error":   fmt.Sprintf("apply patch failed: %s", string(output)),
		})
	}

	// Extract modified files from patch
	filesModified := extractFilesFromPatch(req.Patch)

	return json.Marshal(map[string]any{
		"success":        true,
		"files_modified": filesModified,
		"output":         strings.TrimSpace(string(output)),
	})
}

// RunCommand executes a shell command with timeout.
// Input: {"command": "go test ./...", "timeout": 120}
// Output: {"stdout": "...", "stderr": "...", "exit_code": 0, "duration_ms": 5000}
// SECURITY: Checks command against denylist, rejects dangerous commands.
func (t *WorkspaceTools) RunCommand(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for run_command: %w", err)
	}

	// Validate command against denylist
	if err := isDangerousCommand(req.Command); err != nil {
		return nil, err
	}

	// Default timeout: 120 seconds
	timeoutSec := req.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	if timeoutSec > 600 {
		timeoutSec = 600 // Max 10 minutes
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	start := time.Now()

	// Use shell for command execution
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", req.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", req.Command)
	}
	cmd.Dir = workspacePath

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	var stdoutBuf, stderrBuf strings.Builder
	go func() { _, _ = io.Copy(&stdoutBuf, stdout) }()
	go func() { _, _ = io.Copy(&stderrBuf, stderr) }()

	waitErr := cmd.Wait()
	durationMs := int(time.Since(start).Milliseconds())

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	// Collect output (truncate if too large)
	stdoutStr := truncateString(stdoutBuf.String(), 50000)
	stderrStr := truncateString(stderrBuf.String(), 50000)

	t.logger.Debug("run_command", "command", req.Command, "exit_code", exitCode, "duration_ms", durationMs)

	return json.Marshal(map[string]any{
		"stdout":      stdoutStr,
		"stderr":      stderrStr,
		"exit_code":   exitCode,
		"duration_ms": durationMs,
	})
}

// InspectRepo returns repository structure and metadata.
// Input: {}
// Output: {"root": "/path", "total_files": 150, "total_dirs": 20, "languages": {"Go": 80, "TypeScript": 40}, "package_manager": "go", "framework": "chi"}
func (t *WorkspaceTools) InspectRepo(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	files, dirs, languages := countFileStats(workspacePath)

	// Detect package manager
	packageManager := detectPackageManager(workspacePath)

	// Detect framework
	framework := detectFramework(workspacePath)

	// Find key files
	keyFiles := findKeyFiles(workspacePath)

	return json.Marshal(map[string]any{
		"root":            workspacePath,
		"total_files":     files,
		"total_dirs":      dirs,
		"languages":       languages,
		"package_manager": packageManager,
		"framework":       framework,
		"key_files":       keyFiles,
	})
}

// GetGitDiff returns current uncommitted changes.
// Input: {}
// Output: {"diff": "...", "files_changed": ["file1.go", "file2.go"], "insertions": 50, "deletions": 20}
func (t *WorkspaceTools) GetGitDiff(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	// Get diff
	cmd := exec.CommandContext(ctx, "git", "-C", workspacePath, "diff", "--stat")
	statOutput, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --stat: %w", err)
	}

	// Get full diff
	cmd = exec.CommandContext(ctx, "git", "-C", workspacePath, "diff")
	diffOutput, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	// Parse stat for insertions/deletions and file list
	filesChanged, insertions, deletions := parseGitDiffStat(string(statOutput))

	return json.Marshal(map[string]any{
		"diff":          string(diffOutput),
		"files_changed": filesChanged,
		"insertions":    insertions,
		"deletions":     deletions,
	})
}

// CreateCommit stages all changes and creates a commit.
// Input: {"message": "feat: add settings page"}
// Output: {"success": true, "commit_hash": "abc123"}
func (t *WorkspaceTools) CreateCommit(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for create_commit: %w", err)
	}

	if strings.TrimSpace(req.Message) == "" {
		return nil, fmt.Errorf("commit message is required")
	}

	// Stage all changes
	cmd := exec.CommandContext(ctx, "git", "-C", workspacePath, "add", "-A")
	if output, err := cmd.CombinedOutput(); err != nil {
		return json.Marshal(map[string]any{
			"success": false,
			"error":   fmt.Sprintf("git add failed: %s", string(output)),
		})
	}

	// Create commit
	cmd = exec.CommandContext(ctx, "git", "-C", workspacePath, "commit", "-m", req.Message)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return json.Marshal(map[string]any{
			"success": false,
			"error":   fmt.Sprintf("git commit failed: %s", string(output)),
		})
	}

	// Extract commit hash
	commitHash := ""
	cmd = exec.CommandContext(ctx, "git", "-C", workspacePath, "rev-parse", "HEAD")
	hashOutput, err := cmd.Output()
	if err == nil {
		commitHash = strings.TrimSpace(string(hashOutput))
	}

	return json.Marshal(map[string]any{
		"success":     true,
		"commit_hash": commitHash,
		"output":      strings.TrimSpace(string(output)),
	})
}

// RunTests runs the project's test suite.
// Input: {"command": "go test ./...", "timeout": 300}
// Output: {"passed": true, "total": 50, "failed": 0, "skipped": 2, "duration_ms": 15000, "output": "..."}
func (t *WorkspaceTools) RunTests(ctx context.Context, workspacePath string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Command string `json:"command,omitempty"`
		Timeout int    `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for run_tests: %w", err)
	}

	testCommand := req.Command
	if testCommand == "" {
		testCommand = detectTestCommand(workspacePath)
	}

	// Validate command against denylist
	if err := isDangerousCommand(testCommand); err != nil {
		return nil, err
	}

	timeoutSec := req.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	if timeoutSec > 600 {
		timeoutSec = 600
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	start := time.Now()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", testCommand)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", testCommand)
	}
	cmd.Dir = workspacePath

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

	outputStr := truncateString(string(output), 50000)
	passed := exitCode == 0

	// Try to parse test counts from output
	total, failed, skipped := parseTestCounts(outputStr)

	t.logger.Debug("run_tests", "command", testCommand, "passed", passed, "duration_ms", durationMs)

	return json.Marshal(map[string]any{
		"passed":      passed,
		"total":       total,
		"failed":      failed,
		"skipped":     skipped,
		"duration_ms": durationMs,
		"output":      outputStr,
		"exit_code":   exitCode,
	})
}

// ---------------------------------------------------------------------------
// HELPER FUNCTIONS
// ---------------------------------------------------------------------------

// resolveWorkspacePath ensures the path is within the workspace directory.
func resolveWorkspacePath(workspacePath, requestedPath string) (string, error) {
	if filepath.IsAbs(requestedPath) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", requestedPath)
	}

	// Clean and join the path
	cleanPath := filepath.Clean(requestedPath)
	if cleanPath == "." || cleanPath == "/" {
		cleanPath = ""
	}

	fullPath := filepath.Join(workspacePath, cleanPath)

	// Resolve symlinks
	resolvedPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		// If the file doesn't exist yet (for write), resolve the parent
		resolvedPath = fullPath
	}

	// Ensure resolved path is within workspace
	workspaceAbs, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	resolvedAbs, err := filepath.Abs(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("resolve target path: %w", err)
	}

	// Add trailing separator for prefix check
	prefix := workspaceAbs + string(filepath.Separator)
	if !strings.HasPrefix(resolvedAbs, prefix) && resolvedAbs != workspaceAbs {
		return "", fmt.Errorf("path traversal detected: %s escapes workspace %s", requestedPath, workspacePath)
	}

	return resolvedAbs, nil
}

// isDangerousCommand checks if a command is on the denylist.
func isDangerousCommand(cmd string) error {
	lower := strings.ToLower(cmd)

	// Denylist of dangerous commands and patterns
	dangerous := []string{
		// Filesystem destruction
		"rm -rf /", "rm -rf /*", "rm -rf ~", "dd if=/dev/zero",
		"mkfs", "mke2fs", "mkfs.ext", "mkfs.btrfs",
		"> /dev/sda", "> /dev/hda", "> /dev/nvme",
		":(){ :|:& };:", // fork bomb

		// Permission escalation
		"sudo ", "su -", "su root", "passwd",

		// Destructive SSH operations
		"ssh-keygen -f /etc/ssh",

		// Dangerous curl/wget patterns
		"curl | sh", "curl | bash", "wget | sh", "wget | bash",
		"curl.*|.*sh", "wget.*|.*bash",
	}

	for _, pattern := range dangerous {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return fmt.Errorf("dangerous command rejected: contains forbidden pattern %q", pattern)
		}
	}

	// Reject pipe-to-shell patterns
	if matchesPipeToShell(lower) {
		return fmt.Errorf("dangerous command rejected: pipe-to-shell pattern detected")
	}

	// Reject commands that try to escape workspace
	if strings.Contains(lower, "cd /") && !strings.Contains(lower, "cd /workspace") && !strings.Contains(lower, "cd /app") {
		// Allow cd to standard workspace directories only
		for _, dangerous := range []string{"cd /etc", "cd /var", "cd /usr", "cd /home", "cd /root", "cd /sys", "cd /proc", "cd /dev"} {
			if strings.Contains(lower, dangerous) {
				return fmt.Errorf("dangerous command rejected: attempts to access system directory %s", dangerous)
			}
		}
	}

	return nil
}

// isDangerousQuery checks if a search query could be harmful.
func isDangerousQuery(query string) bool {
	// Reject queries that look like filesystem traversal or binary data
	if strings.Contains(query, "\x00") {
		return true
	}
	return false
}

// matchesPipeToShell checks for dangerous pipe-to-shell patterns.
func matchesPipeToShell(cmd string) bool {
	// Simple heuristic: curl/wget followed by pipe to sh/bash
	if (strings.Contains(cmd, "curl") || strings.Contains(cmd, "wget")) &&
		(strings.Contains(cmd, "| sh") || strings.Contains(cmd, "| bash") || strings.Contains(cmd, "| /bin/sh")) {
		return true
	}
	return false
}

// hasRipgrep checks if ripgrep (rg) is installed.
func hasRipgrep() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

// isLiteralQuery returns true if the query appears to be a literal string.
func isLiteralQuery(query string) bool {
	// If the query contains regex special characters, it's probably a regex
	specialChars := `.*+?^$()[]{}|\`
	for _, ch := range specialChars {
		if strings.Contains(query, string(ch)) {
			return false
		}
	}
	return true
}

// countFileStats returns file count, dir count, and language breakdown.
func countFileStats(root string) (files, dirs int, languages map[string]int) {
	languages = make(map[string]int)

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip unreadable entries
		}

		// Skip hidden dirs and common non-source directories
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			if name == "node_modules" || name == "vendor" || name == "dist" || name == "build" || name == "tmp" || name == "bin" {
				return filepath.SkipDir
			}
			dirs++
			return nil
		}

		files++
		lang := detectLanguage(name)
		if lang != "" {
			languages[lang]++
		}
		return nil
	})

	return files, dirs, languages
}

// detectLanguage maps file extensions to language names.
func detectLanguage(filename string) string {
	if filename == "Gemfile" {
		return "Ruby"
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".go":
		return "Go"
	case ".js", ".jsx":
		return "JavaScript"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".py":
		return "Python"
	case ".java":
		return "Java"
	case ".rb":
		return "Ruby"
	case ".rs":
		return "Rust"
	case ".c", ".h":
		return "C"
	case ".cpp", ".cc", ".hpp":
		return "C++"
	case ".cs":
		return "C#"
	case ".php":
		return "PHP"
	case ".swift":
		return "Swift"
	case ".kt":
		return "Kotlin"
	case ".scala":
		return "Scala"
	case ".r":
		return "R"
	case ".m":
		return "Objective-C"
	case ".sh":
		return "Shell"
	case ".sql":
		return "SQL"
	case ".html", ".htm":
		return "HTML"
	case ".css", ".scss", ".sass", ".less":
		return "CSS"
	case ".json":
		return "JSON"
	case ".xml", ".yaml", ".yml", ".toml":
		return "Config"
	case ".md", ".markdown":
		return "Markdown"
	case ".dockerfile":
		return "Dockerfile"
	default:
		if strings.HasSuffix(filename, "Dockerfile") {
			return "Dockerfile"
		}
		return ""
	}
}

// detectPackageManager detects the package manager used in the project.
func detectPackageManager(workspacePath string) string {
	files := map[string]string{
		"go.mod":           "go",
		"package.json":     "npm",
		"yarn.lock":        "yarn",
		"pnpm-lock.yaml":   "pnpm",
		"Cargo.toml":       "cargo",
		"requirements.txt": "pip",
		"pyproject.toml":   "poetry",
		"Pipfile":          "pipenv",
		"Gemfile":          "bundler",
		"pom.xml":          "maven",
		"build.gradle":     "gradle",
		"composer.json":    "composer",
	}
	for file, manager := range files {
		if _, err := os.Stat(filepath.Join(workspacePath, file)); err == nil {
			return manager
		}
	}
	return "unknown"
}

// detectFramework attempts to detect the web framework used.
func detectFramework(workspacePath string) string {
	// Check go.mod for Go frameworks
	if data, err := os.ReadFile(filepath.Join(workspacePath, "go.mod")); err == nil {
		content := string(data)
		if strings.Contains(content, "chi") {
			return "chi"
		}
		if strings.Contains(content, "echo") {
			return "echo"
		}
		if strings.Contains(content, "gin") {
			return "gin"
		}
		if strings.Contains(content, "fiber") {
			return "fiber"
		}
		if strings.Contains(content, "gorilla") {
			return "gorilla/mux"
		}
	}

	// Check package.json for JS frameworks
	if data, err := os.ReadFile(filepath.Join(workspacePath, "package.json")); err == nil {
		content := string(data)
		if strings.Contains(content, "next") {
			return "next"
		}
		if strings.Contains(content, "react") {
			return "react"
		}
		if strings.Contains(content, "vue") {
			return "vue"
		}
		if strings.Contains(content, "svelte") {
			return "svelte"
		}
		if strings.Contains(content, "angular") {
			return "angular"
		}
		if strings.Contains(content, "express") {
			return "express"
		}
	}

	return ""
}

// findKeyFiles finds important configuration files in the repository.
func findKeyFiles(workspacePath string) []string {
	keyFilePatterns := []string{
		"go.mod", "go.sum", "package.json", "Dockerfile", "docker-compose.yml",
		"Makefile", "README.md", ".gitignore", ".github/workflows",
	}

	var found []string
	for _, pattern := range keyFilePatterns {
		matches, _ := filepath.Glob(filepath.Join(workspacePath, pattern))
		for _, m := range matches {
			rel, _ := filepath.Rel(workspacePath, m)
			found = append(found, rel)
		}
	}
	return found
}

// extractFilesFromPatch extracts the list of modified files from a unified diff.
func extractFilesFromPatch(patch string) []string {
	seen := make(map[string]bool)
	var files []string

	scanner := bufio.NewScanner(strings.NewReader(patch))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "--- a/") || strings.HasPrefix(line, "+++ b/") {
			file := strings.TrimPrefix(line, "--- a/")
			file = strings.TrimPrefix(file, "+++ b/")
			if file != "/dev/null" && !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}
	return files
}

// parseGitDiffStat parses git diff --stat output.
func parseGitDiffStat(stat string) (files []string, insertions, deletions int) {
	scanner := bufio.NewScanner(strings.NewReader(stat))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.Contains(line, "files changed") {
			continue
		}
		// Parse " file.go | 10 +++++-----"
		parts := strings.Split(line, "|")
		if len(parts) >= 1 {
			file := strings.TrimSpace(parts[0])
			if file != "" && !strings.Contains(file, "...") {
				files = append(files, file)
			}
		}
	}
	return files, 0, 0
}

// parseTestCounts parses test output for total/failed/skipped counts.
func parseTestCounts(output string) (total, failed, skipped int) {
	// Try to find patterns like "50 tests passed, 2 skipped" or "PASS: 50, FAIL: 0"
	for _, line := range strings.Split(output, "\n") {
		line = strings.ToLower(line)

		// Go test format: "ok  	package	0.5s"
		if strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "fail ") {
			total++
		}

		// Parse "--- FAIL:" lines
		if strings.Contains(line, "--- fail:") {
			failed++
		}
		// Parse "--- SKIP:" lines
		if strings.Contains(line, "--- skip:") {
			skipped++
		}
	}
	return total, failed, skipped
}

// detectTestCommand determines the test command for a project.
func detectTestCommand(workspacePath string) string {
	if _, err := os.Stat(filepath.Join(workspacePath, "go.mod")); err == nil {
		return "go test ./..."
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "package.json")); err == nil {
		return "npm test"
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "Cargo.toml")); err == nil {
		return "cargo test"
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "requirements.txt")); err == nil {
		return "python -m pytest"
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "pyproject.toml")); err == nil {
		return "pytest"
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "pom.xml")); err == nil {
		return "mvn test"
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "build.gradle")); err == nil {
		return "./gradlew test"
	}
	return "echo 'No test command detected'"
}

// truncateString truncates a string to maxLen, appending ... if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [output truncated]"
}
