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
	id             string
	workspaceID    string
	repositoryPath string
	worktreePath   string
	branch         string
	base           string
	status         string
	createdAt      time.Time
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

// CreateWorkspace creates a new local workspace by cloning the repository
// and setting up a git worktree for the specified branch.
func (p *LocalProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	sessionID := generateSessionID()
	sessionDir := filepath.Join(p.baseDir, sessionID)
	repoDir := filepath.Join(sessionDir, "repo")

	worktreeName := req.WorktreeName
	if worktreeName == "" {
		worktreeName = "workspace"
	}
	worktreePath := filepath.Join(sessionDir, worktreeName)

	branch := req.Branch
	if branch == "" {
		branch = "agent/" + sessionID
	}
	base := req.BaseBranch
	if base == "" {
		base = "HEAD"
	}

	backend := vcs.NewGitBackend(nil)
	if err := backend.CloneOrFetch(ctx, vcs.CloneRequest{
		URL:  req.CloneURL,
		Path: repoDir,
		Env:  req.Env,
	}); err != nil {
		_ = os.RemoveAll(sessionDir)
		return nil, fmt.Errorf("prepare repository: %w", err)
	}
	if err := backend.CreateWorkspace(ctx, vcs.WorkspaceRequest{
		RepositoryPath: repoDir,
		WorkspacePath:  worktreePath,
		Name:           branch,
		Base:           base,
	}); err != nil {
		_ = os.RemoveAll(sessionDir)
		return nil, fmt.Errorf("create VCS workspace: %w", err)
	}

	sess := &localSession{
		id:             sessionID,
		workspaceID:    req.RepositoryID,
		repositoryPath: repoDir,
		worktreePath:   worktreePath,
		branch:         branch,
		base:           base,
		status:         "ready",
		createdAt:      time.Now(),
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
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}

	backend := vcs.NewGitBackend(nil)
	if err := backend.RemoveWorkspace(ctx, vcs.WorkspaceRequest{
		RepositoryPath: sess.repositoryPath,
		WorkspacePath:  sess.worktreePath,
		Name:           sess.branch,
		Base:           sess.base,
	}); err != nil {
		return fmt.Errorf("remove VCS workspace: %w", err)
	}

	sessionDir := filepath.Join(p.baseDir, sessionID)
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("remove session directory: %w", err)
	}

	p.mu.Lock()
	if current, exists := p.sessions[sessionID]; exists {
		current.status = "destroyed"
		delete(p.sessions, sessionID)
	}
	p.mu.Unlock()
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
