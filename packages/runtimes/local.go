package runtimes

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
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
	worktreePath string
	status       string
	createdAt    time.Time
}

// NewLocalProvider creates a new local runtime provider.
// baseDir is the root directory where all workspace worktrees will be stored.
func NewLocalProvider(baseDir string) *LocalProvider {
	return &LocalProvider{
		baseDir:  baseDir,
		sessions: make(map[string]*localSession),
	}
}

// CreateWorkspace creates a new local workspace by cloning the repository
// and setting up a git worktree for the specified branch.
func (p *LocalProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	sessionID := generateSessionID()
	worktreePath := filepath.Join(p.baseDir, sessionID, req.WorktreeName)

	if err := os.MkdirAll(worktreePath, 0755); err != nil {
		return nil, fmt.Errorf("create worktree directory: %w", err)
	}

	// Clone the repository if not already cloned
	repoDir := filepath.Join(p.baseDir, sessionID, "repo")
	cloneCmd := exec.CommandContext(ctx, "git", "clone", req.CloneURL, repoDir)
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git clone: %w (output: %s)", err, string(out))
	}

	// Create worktree
	wtCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "worktree", "add", "-B", req.Branch, worktreePath, req.BaseBranch)
	if out, err := wtCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git worktree add: %w (output: %s)", err, string(out))
	}

	sess := &localSession{
		id:           sessionID,
		workspaceID:  req.RepositoryID,
		worktreePath: worktreePath,
		status:       "ready",
		createdAt:    time.Now(),
	}

	p.mu.Lock()
	p.sessions[sessionID] = sess
	p.mu.Unlock()

	return &Session{
		ID:           sessionID,
		WorkspaceID:  req.RepositoryID,
		Status:       "ready",
		Provider:     "local",
		WorktreePath: worktreePath,
		CreatedAt:    sess.createdAt,
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

	execCmd := exec.CommandContext(ctx, "sh", "-c", cmd.Command)
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

	fullPath := filepath.Join(sess.worktreePath, path)
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

	fullPath := filepath.Join(sess.worktreePath, path)
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

// Snapshot creates a git commit in the workspace as a snapshot point.
func (p *LocalProvider) Snapshot(ctx context.Context, sessionID string) (*Snapshot, error) {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	// Stage all changes
	addCmd := exec.CommandContext(ctx, "git", "-C", sess.worktreePath, "add", "-A")
	if out, err := addCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git add: %w (output: %s)", err, string(out))
	}

	// Create snapshot commit
	commitHash := fmt.Sprintf("snapshot-%d", time.Now().Unix())
	commitCmd := exec.CommandContext(ctx, "git", "-C", sess.worktreePath, "commit", "-m", "snapshot: "+commitHash, "--allow-empty")
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("git commit: %w (output: %s)", err, string(out))
	}

	return &Snapshot{
		ID:          commitHash,
		SessionID:   sessionID,
		GitCommit:   commitHash,
		Description: "Local snapshot at " + time.Now().Format(time.RFC3339),
		CreatedAt:   time.Now(),
	}, nil
}

// Restore resets the workspace to a snapshot using git reset.
func (p *LocalProvider) Restore(ctx context.Context, sessionID string, snap *Snapshot) error {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	resetCmd := exec.CommandContext(ctx, "git", "-C", sess.worktreePath, "reset", "--hard", snap.GitCommit)
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset: %w (output: %s)", err, string(out))
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
		defer f.Close()

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
