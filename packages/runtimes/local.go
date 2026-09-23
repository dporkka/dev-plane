package runtimes

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ai-dev-control-plane/vcs"
)

// LocalProvider implements the Provider interface for trusted local mode.
// It executes commands directly on the host machine and manipulates files
// in a designated base directory. This is suitable for local development
// and trusted environments only.
type LocalProvider struct {
	baseDir  string
	sessions map[string]*localSession
	mu       sync.RWMutex
}

type localSession struct {
	id           string
	workspaceID  string
	repositoryPath string
	worktreePath string
	backend      vcs.Backend
	status       string
	createdAt    time.Time
}

// NewLocalProvider creates a new local runtime provider.
// baseDir is the root directory where all workspace worktrees will be stored.
// The directory is ensured to exist and, when possible, backed by tmpfs.
func NewLocalProvider(baseDir string) *LocalProvider {
	baseDir, _ = EnsureTmpfsBaseDir(baseDir)
	return &LocalProvider{
		baseDir:  baseDir,
		sessions: make(map[string]*localSession),
	}
}

// CreateWorkspace creates a new local workspace using the repository's
// configured Git or Jujutsu backend.
func (p *LocalProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	if req.CloneURL == "" {
		return nil, fmt.Errorf("clone url is required")
	}
	if req.WorktreeName == "" {
		return nil, fmt.Errorf("worktree name is required")
	}

	backend, err := vcs.NewBackend(req.VCSBackend, nil)
	if err != nil {
		return nil, err
	}

	sessionID := generateSessionID()
	sessionDir := filepath.Join(p.baseDir, sessionID)
	repoDir := filepath.Join(sessionDir, "repo")
	worktreePath := filepath.Join(sessionDir, req.WorktreeName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(sessionDir)
		}
	}()

	if err := backend.CloneOrFetch(ctx, vcs.CloneRequest{
		URL: req.CloneURL,
		Path: repoDir,
		Env: req.Env,
	}); err != nil {
		return nil, fmt.Errorf("%s clone repository: %w", backend.Name(), err)
	}

	base := req.BaseBranch
	if base == "" {
		base = "main"
	}
	if backend.Name() == "jj" && !strings.Contains(base, "@") {
		base += "@origin"
	}
	branch := req.Branch
	if branch == "" {
		branch = req.WorktreeName
	}
	if err := backend.CreateWorkspace(ctx, vcs.WorkspaceRequest{
		RepositoryPath: repoDir,
		WorkspacePath:  worktreePath,
		Name:           req.WorktreeName,
		Base:           base,
	}); err != nil {
		return nil, fmt.Errorf("%s create workspace: %w", backend.Name(), err)
	}

	now := time.Now()
	sess := &localSession{
		id:             sessionID,
		workspaceID:    req.RepositoryID,
		repositoryPath: repoDir,
		worktreePath:   worktreePath,
		backend:        backend,
		status:         "ready",
		createdAt:      now,
	}
	p.mu.Lock()
	p.sessions[sessionID] = sess
	p.mu.Unlock()
	cleanup = false

	return &Session{
		ID:           sessionID,
		WorkspaceID:  req.RepositoryID,
		Status:       "ready",
		Provider:     "local",
		WorktreePath: worktreePath,
		CreatedAt:    now,
	}, nil
}

// DestroyWorkspace removes the workspace directory and session state.
func (p *LocalProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	p.mu.Lock()
	sess, ok := p.sessions[sessionID]
	if ok {
		sess.status = "destroyed"
		delete(p.sessions, sessionID)
	}
	p.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	sessionDir := filepath.Join(p.baseDir, sessionID)
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("remove session directory: %w", err)
	}
	return nil
}

// ExecuteCommand runs a command in the workspace directory.
func (p *LocalProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error) {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	if sess.status != "ready" && sess.status != "running" {
		return nil, fmt.Errorf("%w: status is %s", ErrSessionNotReady, sess.status)
	}

	dir := sess.worktreePath
	if cmd.Dir != "" {
		dir = filepath.Join(dir, cmd.Dir)
	}

	timeout := cmd.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var execCmd *exec.Cmd
	if len(cmd.Args) > 0 {
		if err := ValidateCommandArgs(cmd.Args); err != nil {
			return nil, fmt.Errorf("invalid command args: %w", err)
		}
		execCmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)
	} else if cmd.UnsafeShell {
		// Trusted system callers may pass a raw shell string.
		execCmd = exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	} else {
		// Fallback for legacy callers that still provide a shell string.
		// Reject obvious shell metacharacters before invoking the shell.
		if _, err := ParseCommandString(cmd.Command); err != nil {
			return nil, fmt.Errorf("invalid command: %w", err)
		}
		execCmd = exec.CommandContext(ctx, "sh", "-c", cmd.Command)
	}
	execCmd.Dir = dir
	execCmd.Env = os.Environ()
	for k, v := range cmd.Env {
		execCmd.Env = append(execCmd.Env, k+"="+v)
	}

	start := time.Now()
	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	err := execCmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			exitCode = -1
			return nil, fmt.Errorf("%w after %v", ErrCommandTimeout, timeout)
		}
	}

	p.mu.Lock()
	if sess, ok := p.sessions[sessionID]; ok {
		sess.status = "running"
	}
	p.mu.Unlock()

	return &CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

// ReadFile reads a file from the workspace.
func (p *LocalProvider) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	rel, err := cleanRelativePath(path)
	if err != nil {
		return nil, err
	}
	fullPath := filepath.Join(sess.worktreePath, rel)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read file %q: %w", path, err)
	}
	return data, nil
}

// WriteFile writes data to a file in the workspace.
func (p *LocalProvider) WriteFile(ctx context.Context, sessionID, path string, data []byte) error {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	rel, err := cleanRelativePath(path)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(sess.worktreePath, rel)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("create parent directories for %q: %w", path, err)
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("write file %q: %w", path, err)
	}
	return nil
}

// ApplyPatch applies a unified diff patch in the workspace using git apply.
func (p *LocalProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", sess.worktreePath, "apply", "-")
	cmd.Stdin = bytes.NewReader([]byte(patch))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git apply: %w (output: %s)", err, string(out))
	}
	return nil
}

// Snapshot captures the current workspace through its configured VCS backend.
func (p *LocalProvider) Snapshot(ctx context.Context, sessionID string) (*Snapshot, error) {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	revision, err := sess.backend.Snapshot(ctx, sess.worktreePath, "snapshot: "+sessionID)
	if err != nil {
		return nil, fmt.Errorf("%s snapshot: %w", sess.backend.Name(), err)
	}
	now := time.Now()
	return &Snapshot{
		ID:          revision.CommitID,
		SessionID:   sessionID,
		GitCommit:   revision.CommitID,
		Description: fmt.Sprintf("%s snapshot at %s", sess.backend.Name(), now.Format(time.RFC3339)),
		CreatedAt:   now,
	}, nil
}

// Restore returns the workspace to the snapshot revision using its configured VCS backend.
func (p *LocalProvider) Restore(ctx context.Context, sessionID string, snap *Snapshot) error {
	if snap == nil || snap.GitCommit == "" {
		return fmt.Errorf("snapshot git commit is required")
	}
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	if err := sess.backend.Restore(ctx, sess.worktreePath, snap.GitCommit); err != nil {
		return fmt.Errorf("%s restore: %w", sess.backend.Name(), err)
	}
	return nil
}

// GetStatus returns the current status of a local session.
func (p *LocalProvider) GetStatus(ctx context.Context, sessionID string) (*SessionStatus, error) {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	return &SessionStatus{
		SessionID:  sessionID,
		Status:     sess.status,
		LastActive: time.Now(),
	}, nil
}

// StreamLogs returns a channel that streams command output from the session.
// In local mode, this reads from a log file in the session directory.
func (p *LocalProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan LogLine, error) {
	p.mu.RLock()
	_, ok := p.sessions[sessionID]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	ch := make(chan LogLine, 100)

	// In local mode, stream logs from a simple log file
	go func() {
		defer close(ch)

		logFile := filepath.Join(p.baseDir, sessionID, "session.log")
		f, err := os.Open(logFile)
		if err != nil {
			// Log file may not exist; just return
			return
		}
		defer func() {
			_ = f.Close()
		}()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case ch <- LogLine{
				Timestamp: time.Now(),
				Stream:    "stdout",
				Message:   scanner.Text(),
			}:
			}
		}
	}()

	return ch, nil
}

// generateSessionID creates a simple session identifier based on timestamp.
func generateSessionID() string {
	return fmt.Sprintf("local-%d", time.Now().UnixNano())
}

var _ Provider = (*LocalProvider)(nil)
