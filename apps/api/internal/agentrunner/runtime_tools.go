package agentrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/runtimes"
)

type runtimeAttacher interface {
	AttachSession(ctx context.Context, sessionID, workspaceID string) (*runtimes.Session, error)
}

func (r *Runner) runtimeProviderForWorkspace(ctx context.Context, workspace *models.Workspace) (runtimes.Provider, string, error) {
	if workspace == nil {
		return nil, "", nil
	}
	providerName := strings.ToLower(strings.TrimSpace(workspace.RuntimeProvider))
	if providerName == "" || providerName == "local" || providerName == "unprovisioned" {
		return nil, "", nil
	}
	if workspace.RuntimeSessionID == nil || *workspace.RuntimeSessionID == "" {
		return nil, "", fmt.Errorf("runtime session id is missing for %s workspace %s", providerName, workspace.ID)
	}
	if r.runtimes == nil {
		r.runtimes = map[string]runtimes.Provider{}
	}
	provider := r.runtimes[providerName]
	if provider == nil {
		switch providerName {
		case "docker":
			created, err := runtimes.NewDockerProvider(agentRuntimeBaseDir())
			if err != nil {
				return nil, "", err
			}
			provider = created
			r.runtimes[providerName] = provider
		default:
			return nil, "", fmt.Errorf("unsupported workspace runtime provider %q", providerName)
		}
	}
	if attacher, ok := provider.(runtimeAttacher); ok {
		if _, err := attacher.AttachSession(ctx, *workspace.RuntimeSessionID, workspace.ID); err != nil {
			return nil, "", fmt.Errorf("attach %s runtime session: %w", providerName, err)
		}
	}
	return provider, *workspace.RuntimeSessionID, nil
}

func agentRuntimeBaseDir() string {
	if baseDir := os.Getenv("WORKSPACE_BASE_DIR"); baseDir != "" {
		return baseDir
	}
	return filepath.Join(os.TempDir(), "ai-dev-control-plane-workspaces")
}

func (r *Runner) executeRuntimeTool(ctx context.Context, provider runtimes.Provider, sessionID, toolName string, input json.RawMessage) (json.RawMessage, error) {
	switch toolName {
	case "read_file":
		return runtimeReadFile(ctx, provider, sessionID, input)
	case "write_file":
		return runtimeWriteFile(ctx, provider, sessionID, input)
	case "search_files":
		return runtimeSearchFiles(ctx, provider, sessionID, input)
	case "list_directory":
		return runtimeListDirectory(ctx, provider, sessionID, input)
	case "apply_patch":
		return runtimeApplyPatch(ctx, provider, sessionID, input)
	case "run_command":
		return runtimeRunCommand(ctx, provider, sessionID, input)
	case "inspect_repo":
		return runtimeInspectRepo(ctx, provider, sessionID)
	case "get_git_diff":
		return runtimeGetGitDiff(ctx, provider, sessionID)
	case "create_commit":
		return runtimeCreateCommit(ctx, provider, sessionID, input)
	case "run_tests":
		return runtimeRunTests(ctx, provider, sessionID, input)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

func runtimeReadFile(ctx context.Context, provider runtimes.Provider, sessionID string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for read_file: %w", err)
	}
	data, err := provider.ReadFile(ctx, sessionID, req.Path)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file") {
			return json.Marshal(map[string]any{"error": fmt.Sprintf("file not found: %s", req.Path)})
		}
		return nil, err
	}
	content := string(data)
	lines := strings.Count(content, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		lines++
	}
	return json.Marshal(map[string]any{
		"content": content,
		"size":    len(data),
		"lines":   lines,
	})
}

func runtimeWriteFile(ctx context.Context, provider runtimes.Provider, sessionID string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for write_file: %w", err)
	}
	if err := provider.WriteFile(ctx, sessionID, req.Path, []byte(req.Content)); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"success":       true,
		"bytes_written": len(req.Content),
	})
}

func runtimeSearchFiles(ctx context.Context, provider runtimes.Provider, sessionID string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Query string `json:"query"`
		Path  string `json:"path,omitempty"`
		Glob  string `json:"glob,omitempty"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for search_files: %w", err)
	}
	if req.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if strings.Contains(req.Query, "\x00") {
		return nil, fmt.Errorf("dangerous search query rejected: %s", req.Query)
	}
	path := req.Path
	if path == "" {
		path = "."
	}
	command := "if command -v rg >/dev/null 2>&1; then rg --line-number --no-heading --with-filename --color=never"
	if req.Glob != "" {
		command += " --glob " + shellQuote(req.Glob)
	}
	command += " -- " + shellQuote(req.Query) + " " + shellQuote(path)
	command += "; else grep -R -n -H -- " + shellQuote(req.Query) + " " + shellQuote(path)
	command += "; fi; code=$?; [ $code -eq 0 ] || [ $code -eq 1 ]"
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{Command: command, Timeout: 60 * time.Second})
	if err != nil {
		return nil, err
	}

	type searchResult struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Content string `json:"content"`
	}
	var results []searchResult
	for _, line := range strings.Split(result.Stdout, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		var lineNum int
		_, _ = fmt.Sscanf(parts[1], "%d", &lineNum)
		results = append(results, searchResult{File: strings.TrimPrefix(parts[0], "./"), Line: lineNum, Content: parts[2]})
	}
	if results == nil {
		results = []searchResult{}
	}
	return json.Marshal(map[string]any{"results": results, "count": len(results)})
}

func runtimeListDirectory(ctx context.Context, provider runtimes.Provider, sessionID string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Path string `json:"path,omitempty"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for list_directory: %w", err)
	}
	if req.Path == "" {
		req.Path = "."
	}
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Dir: req.Path,
		Command: `for p in ./*; do
  [ -e "$p" ] || continue
  name=${p#./}
  if [ -d "$p" ]; then
    printf '%s\t%s\t%s\n' "$name" directory 0
  else
    size=$(wc -c < "$p" 2>/dev/null | tr -d ' ')
    printf '%s\t%s\t%s\n' "$name" file "${size:-0}"
  fi
done`,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	type dirEntry struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Size int64  `json:"size,omitempty"`
	}
	var entries []dirEntry
	for _, line := range strings.Split(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		var size int64
		_, _ = fmt.Sscanf(parts[2], "%d", &size)
		entries = append(entries, dirEntry{Name: parts[0], Type: parts[1], Size: size})
	}
	if entries == nil {
		entries = []dirEntry{}
	}
	return json.Marshal(map[string]any{"entries": entries, "path": req.Path, "count": len(entries)})
}

func runtimeApplyPatch(ctx context.Context, provider runtimes.Provider, sessionID string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for apply_patch: %w", err)
	}
	if strings.TrimSpace(req.Patch) == "" {
		return nil, fmt.Errorf("patch is empty")
	}
	if err := provider.ApplyPatch(ctx, sessionID, req.Patch); err != nil {
		return json.Marshal(map[string]any{"success": false, "error": err.Error()})
	}
	return json.Marshal(map[string]any{
		"success":        true,
		"files_modified": runtimeExtractFilesFromPatch(req.Patch),
		"output":         "",
	})
}

func runtimeRunCommand(ctx context.Context, provider runtimes.Provider, sessionID string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for run_command: %w", err)
	}
	if err := rejectDangerousRuntimeCommand(req.Command); err != nil {
		return nil, err
	}
	timeoutSec := req.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 120
	}
	if timeoutSec > 600 {
		timeoutSec = 600
	}
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Command: req.Command,
		Timeout: time.Duration(timeoutSec) * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"stdout":      truncateRuntimeString(result.Stdout, 50000),
		"stderr":      truncateRuntimeString(result.Stderr, 50000),
		"exit_code":   result.ExitCode,
		"duration_ms": int(result.Duration.Milliseconds()),
	})
}

func runtimeInspectRepo(ctx context.Context, provider runtimes.Provider, sessionID string) (json.RawMessage, error) {
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Command: `find . -path './.git' -prune -o -type f -print | sed 's#^\./##' | head -n 1000`,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	files := 0
	languages := map[string]int{}
	var keyFiles []string
	for _, file := range strings.Split(result.Stdout, "\n") {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		files++
		if lang := runtimeDetectLanguage(file); lang != "" {
			languages[lang]++
		}
		switch file {
		case "go.mod", "go.sum", "package.json", "Dockerfile", "docker-compose.yml", "Makefile", "README.md", ".gitignore":
			keyFiles = append(keyFiles, file)
		}
	}
	packageManager, framework := runtimeDetectProjectShape(ctx, provider, sessionID)
	return json.Marshal(map[string]any{
		"root":            "runtime:" + sessionID,
		"total_files":     files,
		"total_dirs":      0,
		"languages":       languages,
		"package_manager": packageManager,
		"framework":       framework,
		"key_files":       keyFiles,
	})
}

func runtimeGetGitDiff(ctx context.Context, provider runtimes.Provider, sessionID string) (json.RawMessage, error) {
	stat, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{Command: "git diff --stat", Timeout: 30 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("git diff --stat: %w", err)
	}
	diff, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{Command: "git diff", Timeout: 30 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	filesChanged := runtimeParseGitDiffStat(stat.Stdout)
	return json.Marshal(map[string]any{
		"diff":          diff.Stdout,
		"files_changed": filesChanged,
		"insertions":    0,
		"deletions":     0,
	})
}

func runtimeCreateCommit(ctx context.Context, provider runtimes.Provider, sessionID string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for create_commit: %w", err)
	}
	if strings.TrimSpace(req.Message) == "" {
		return nil, fmt.Errorf("commit message is required")
	}
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{
		Command: "git add -A && git -c user.email=dev-plane@example.invalid -c user.name='Dev Plane' commit -m " + shellQuote(req.Message) + " && git rev-parse HEAD",
		Timeout: 60 * time.Second,
	})
	if err != nil || result == nil || result.ExitCode != 0 {
		errText := ""
		if err != nil {
			errText = err.Error()
		}
		if result == nil {
			return json.Marshal(map[string]any{"success": false, "error": errText})
		}
		return json.Marshal(map[string]any{"success": false, "error": strings.TrimSpace(result.Stdout + result.Stderr + errText)})
	}
	lines := strings.Fields(result.Stdout)
	commitHash := ""
	if len(lines) > 0 {
		commitHash = lines[len(lines)-1]
	}
	return json.Marshal(map[string]any{"success": true, "commit_hash": commitHash, "output": strings.TrimSpace(result.Stdout + result.Stderr)})
}

func runtimeRunTests(ctx context.Context, provider runtimes.Provider, sessionID string, input json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Command string `json:"command,omitempty"`
		Timeout int    `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("invalid input for run_tests: %w", err)
	}
	testCommand := req.Command
	if testCommand == "" {
		testCommand = runtimeDetectTestCommand(ctx, provider, sessionID)
	}
	if err := rejectDangerousRuntimeCommand(testCommand); err != nil {
		return nil, err
	}
	timeoutSec := req.Timeout
	if timeoutSec <= 0 {
		timeoutSec = 300
	}
	if timeoutSec > 600 {
		timeoutSec = 600
	}
	result, err := provider.ExecuteCommand(ctx, sessionID, runtimes.Command{Command: testCommand, Timeout: time.Duration(timeoutSec) * time.Second})
	if err != nil {
		return nil, err
	}
	output := truncateRuntimeString(result.Stdout+result.Stderr, 50000)
	total, failed, skipped := runtimeParseTestCounts(output)
	return json.Marshal(map[string]any{
		"passed":      result.ExitCode == 0,
		"total":       total,
		"failed":      failed,
		"skipped":     skipped,
		"duration_ms": int(result.Duration.Milliseconds()),
		"output":      output,
		"exit_code":   result.ExitCode,
	})
}

func runtimeDetectProjectShape(ctx context.Context, provider runtimes.Provider, sessionID string) (string, string) {
	if data, err := provider.ReadFile(ctx, sessionID, "go.mod"); err == nil {
		content := string(data)
		switch {
		case strings.Contains(content, "chi"):
			return "go", "chi"
		case strings.Contains(content, "echo"):
			return "go", "echo"
		case strings.Contains(content, "gin"):
			return "go", "gin"
		default:
			return "go", ""
		}
	}
	if data, err := provider.ReadFile(ctx, sessionID, "package.json"); err == nil {
		content := string(data)
		switch {
		case strings.Contains(content, "next"):
			return "npm", "next"
		case strings.Contains(content, "react"):
			return "npm", "react"
		default:
			return "npm", ""
		}
	}
	return "unknown", ""
}

func runtimeDetectTestCommand(ctx context.Context, provider runtimes.Provider, sessionID string) string {
	for _, candidate := range []struct {
		file    string
		command string
	}{
		{"go.mod", "go test ./..."},
		{"package.json", "npm test"},
		{"Cargo.toml", "cargo test"},
		{"requirements.txt", "python -m pytest"},
		{"pyproject.toml", "pytest"},
		{"pom.xml", "mvn test"},
		{"build.gradle", "./gradlew test"},
	} {
		if _, err := provider.ReadFile(ctx, sessionID, candidate.file); err == nil {
			return candidate.command
		}
	}
	return "echo 'No test command detected'"
}

func runtimeExtractFilesFromPatch(patch string) []string {
	seen := make(map[string]bool)
	var files []string
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "--- a/") || strings.HasPrefix(line, "+++ b/") {
			file := strings.TrimPrefix(strings.TrimPrefix(line, "--- a/"), "+++ b/")
			if file != "/dev/null" && !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}
	return files
}

func runtimeParseGitDiffStat(stat string) []string {
	var files []string
	for _, line := range strings.Split(stat, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "files changed") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) > 0 {
			file := strings.TrimSpace(parts[0])
			if file != "" && !strings.Contains(file, "...") {
				files = append(files, file)
			}
		}
	}
	return files
}

func runtimeParseTestCounts(output string) (total, failed, skipped int) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.ToLower(line)
		if strings.HasPrefix(line, "ok ") || strings.HasPrefix(line, "fail ") {
			total++
		}
		if strings.Contains(line, "--- fail:") {
			failed++
		}
		if strings.Contains(line, "--- skip:") {
			skipped++
		}
	}
	return total, failed, skipped
}

func rejectDangerousRuntimeCommand(cmd string) error {
	lower := strings.ToLower(cmd)
	for _, pattern := range []string{
		"rm -rf /", "rm -rf /*", "rm -rf ~", "dd if=/dev/zero", "mkfs", "mke2fs",
		"mkfs.ext", "mkfs.btrfs", "> /dev/sda", "> /dev/hda", "> /dev/nvme",
		":(){ :|:& };:", "sudo ", "su -", "su root", "passwd", "ssh-keygen -f /etc/ssh",
	} {
		if strings.Contains(lower, pattern) {
			return fmt.Errorf("dangerous command rejected: contains forbidden pattern %q", pattern)
		}
	}
	if (strings.Contains(lower, "curl") || strings.Contains(lower, "wget")) &&
		(strings.Contains(lower, "| sh") || strings.Contains(lower, "| bash") || strings.Contains(lower, "| /bin/sh")) {
		return fmt.Errorf("dangerous command rejected: pipe-to-shell pattern detected")
	}
	return nil
}

func runtimeDetectLanguage(filename string) string {
	if filename == "Gemfile" {
		return "Ruby"
	}
	switch strings.ToLower(filepath.Ext(filename)) {
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
	case ".sh":
		return "Shell"
	case ".sql":
		return "SQL"
	case ".html", ".htm":
		return "HTML"
	case ".css", ".scss", ".sass", ".less":
		return "CSS"
	case ".json", ".xml", ".yaml", ".yml", ".toml":
		return "Config"
	case ".md", ".markdown":
		return "Markdown"
	default:
		if strings.HasSuffix(filename, "Dockerfile") {
			return "Dockerfile"
		}
		return ""
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func truncateRuntimeString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n... [output truncated]"
}
