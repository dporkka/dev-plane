package runtimes

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultDockerImage  = "alpine/git:latest"
	defaultDockerMemory = "512m"
	defaultDockerCPUs   = "1.0"
	defaultDockerPIDs   = "256"
	workspaceDir        = "/workspace"
)

type commandRunner interface {
	Run(ctx context.Context, name string, args []string, opts commandOptions) (commandOutput, error)
	Start(ctx context.Context, name string, args []string, opts commandOptions) (startedCommand, error)
}

type commandOptions struct {
	Dir    string
	Env    map[string]string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type commandOutput struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

type startedCommand interface {
	Wait() error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args []string, opts commandOptions) (commandOutput, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = opts.Dir
	cmd.Env = os.Environ()
	for k, v := range opts.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = opts.Stdin

	var stdout, stderr bytes.Buffer
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	} else {
		cmd.Stdout = &stdout
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	} else {
		cmd.Stderr = &stderr
	}

	start := time.Now()
	err := cmd.Run()
	out := commandOutput{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			out.ExitCode = exitErr.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			out.ExitCode = -1
		}
		return out, err
	}
	return out, nil
}

func (execRunner) Start(ctx context.Context, name string, args []string, opts commandOptions) (startedCommand, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = opts.Dir
	cmd.Env = os.Environ()
	for k, v := range opts.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// DockerProvider implements the Provider interface using Docker containers.
type DockerProvider struct {
	baseDir     string
	image       string
	memoryLimit string
	cpuLimit    string
	pidsLimit   string
	runner      commandRunner
	sessions    map[string]*dockerSession
	mu          sync.RWMutex
}

type dockerSession struct {
	id          string
	workspaceID string
	container   string
	volume      string
	status      string
	createdAt   time.Time
	lastActive  time.Time
}

// NewDockerProvider creates a new Docker runtime provider.
func NewDockerProvider(baseDir string) (*DockerProvider, error) {
	image := os.Getenv("DOCKER_WORKSPACE_IMAGE")
	if image == "" {
		image = defaultDockerImage
	}
	memoryLimit := os.Getenv("DOCKER_WORKSPACE_MEMORY")
	if memoryLimit == "" {
		memoryLimit = defaultDockerMemory
	}
	cpuLimit := os.Getenv("DOCKER_WORKSPACE_CPUS")
	if cpuLimit == "" {
		cpuLimit = defaultDockerCPUs
	}
	pidsLimit := os.Getenv("DOCKER_WORKSPACE_PIDS")
	if pidsLimit == "" {
		pidsLimit = defaultDockerPIDs
	}
	return &DockerProvider{
		baseDir:     baseDir,
		image:       image,
		memoryLimit: memoryLimit,
		cpuLimit:    cpuLimit,
		pidsLimit:   pidsLimit,
		runner:      execRunner{},
		sessions:    make(map[string]*dockerSession),
	}, nil
}

// AttachSession registers an existing Docker runtime session with this
// provider process. Docker session resources use deterministic names, so API
// and worker processes can reconnect from persisted runtime_session_id values.
func (p *DockerProvider) AttachSession(ctx context.Context, sessionID, workspaceID string) (*Session, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	container := "dev-plane-" + sessionID
	volume := "dev-plane-" + sessionID + "-workspace"
	now := time.Now()

	p.mu.Lock()
	if sess, ok := p.sessions[sessionID]; ok {
		p.mu.Unlock()
		return &Session{
			ID:          sess.id,
			WorkspaceID: sess.workspaceID,
			Status:      sess.status,
			Provider:    "docker",
			CreatedAt:   sess.createdAt,
		}, nil
	}
	p.sessions[sessionID] = &dockerSession{
		id:          sessionID,
		workspaceID: workspaceID,
		container:   container,
		volume:      volume,
		status:      "ready",
		createdAt:   now,
		lastActive:  now,
	}
	p.mu.Unlock()

	status, err := p.GetStatus(ctx, sessionID)
	if err != nil {
		p.mu.Lock()
		delete(p.sessions, sessionID)
		p.mu.Unlock()
		return nil, err
	}
	return &Session{
		ID:          sessionID,
		WorkspaceID: workspaceID,
		Status:      status.Status,
		Provider:    "docker",
		CreatedAt:   now,
	}, nil
}

// CreateWorkspace provisions a new Docker-based workspace.
func (p *DockerProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	if req.CloneURL == "" {
		return nil, fmt.Errorf("clone url is required")
	}
	sessionID := generateSessionID()
	container := "dev-plane-" + sessionID
	volume := "dev-plane-" + sessionID + "-workspace"
	sessionDir := filepath.Join(p.baseDir, sessionID)
	stagingRepo := filepath.Join(sessionDir, "repo")

	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(sessionDir)
			_, _ = p.runner.Run(context.Background(), "docker", []string{"rm", "-f", container}, commandOptions{})
			_, _ = p.runner.Run(context.Background(), "docker", []string{"volume", "rm", "-f", volume}, commandOptions{})
		}
	}()

	if out, err := p.runner.Run(ctx, "git", []string{"clone", req.CloneURL, stagingRepo}, commandOptions{}); err != nil {
		return nil, fmt.Errorf("git clone: %w (stderr: %s)", err, out.Stderr)
	}
	if err := p.checkoutBranch(ctx, stagingRepo, req); err != nil {
		return nil, err
	}

	if out, err := p.runner.Run(ctx, "docker", []string{"volume", "create", volume}, commandOptions{}); err != nil {
		return nil, fmt.Errorf("docker volume create: %w (stderr: %s)", err, out.Stderr)
	}

	runArgs := []string{
		"run", "-d",
		"--name", container,
		"--label", "dev-plane.session=" + sessionID,
		"--network", "none",
		"--read-only",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=64m",
		"--mount", "type=volume,source=" + volume + ",target=" + workspaceDir,
		"--memory", valueOrDefault(p.memoryLimit, defaultDockerMemory),
		"--cpus", valueOrDefault(p.cpuLimit, defaultDockerCPUs),
		"--pids-limit", valueOrDefault(p.pidsLimit, defaultDockerPIDs),
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--workdir", workspaceDir,
		"--entrypoint", "sh",
	}
	for k, v := range req.Env {
		runArgs = append(runArgs, "-e", k+"="+v)
	}
	runArgs = append(runArgs, p.image, "-c", "sleep infinity")
	if out, err := p.runner.Run(ctx, "docker", runArgs, commandOptions{}); err != nil {
		return nil, fmt.Errorf("docker run: %w (stderr: %s)", err, out.Stderr)
	}
	if out, err := p.runner.Run(ctx, "docker", []string{"exec", container, "mkdir", "-p", workspaceDir}, commandOptions{}); err != nil {
		return nil, fmt.Errorf("docker mkdir workspace: %w (stderr: %s)", err, out.Stderr)
	}
	// Append "/." explicitly so docker cp copies the contents of the staging
	// directory into /workspace. filepath.Join(stagingRepo, ".") collapses to
	// stagingRepo, which would copy the directory itself as /workspace/repo.
	srcPath := stagingRepo + string(filepath.Separator) + "."
	if out, err := p.runner.Run(ctx, "docker", []string{"cp", srcPath, container + ":" + workspaceDir + "/"}, commandOptions{}); err != nil {
		return nil, fmt.Errorf("docker copy workspace: %w (stderr: %s)", err, out.Stderr)
	}

	now := time.Now()
	sess := &dockerSession{
		id:          sessionID,
		workspaceID: req.RepositoryID,
		container:   container,
		volume:      volume,
		status:      "ready",
		createdAt:   now,
		lastActive:  now,
	}
	p.mu.Lock()
	p.sessions[sessionID] = sess
	p.mu.Unlock()
	cleanupOnError = false
	_ = os.RemoveAll(stagingRepo)

	return &Session{
		ID:          sessionID,
		WorkspaceID: req.RepositoryID,
		Status:      "ready",
		Provider:    "docker",
		CreatedAt:   now,
	}, nil
}

func (p *DockerProvider) checkoutBranch(ctx context.Context, repoDir string, req CreateRequest) error {
	if req.BaseBranch != "" {
		if out, err := p.runner.Run(ctx, "git", []string{"-C", repoDir, "checkout", req.BaseBranch}, commandOptions{}); err != nil {
			return fmt.Errorf("git checkout base branch: %w (stderr: %s)", err, out.Stderr)
		}
	}
	if req.Branch == "" {
		return nil
	}
	base := "HEAD"
	if req.BaseBranch != "" {
		base = req.BaseBranch
	}
	if out, err := p.runner.Run(ctx, "git", []string{"-C", repoDir, "checkout", "-B", req.Branch, base}, commandOptions{}); err != nil {
		return fmt.Errorf("git checkout workspace branch: %w (stderr: %s)", err, out.Stderr)
	}
	return nil
}

// DestroyWorkspace removes the Docker container and cleans up local staging data.
func (p *DockerProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	sess, err := p.session(sessionID)
	if err != nil {
		return err
	}
	out, rmErr := p.runner.Run(ctx, "docker", []string{"rm", "-f", sess.container}, commandOptions{})
	if rmErr != nil {
		return fmt.Errorf("docker rm: %w (stderr: %s)", rmErr, out.Stderr)
	}
	if out, err := p.runner.Run(ctx, "docker", []string{"volume", "rm", "-f", sess.volume}, commandOptions{}); err != nil {
		return fmt.Errorf("docker volume rm: %w (stderr: %s)", err, out.Stderr)
	}
	_ = os.RemoveAll(filepath.Join(p.baseDir, sessionID))

	p.mu.Lock()
	delete(p.sessions, sessionID)
	p.mu.Unlock()
	return nil
}

// ExecuteCommand runs a command inside the Docker container.
func (p *DockerProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error) {
	sess, err := p.session(sessionID)
	if err != nil {
		return nil, err
	}
	workdir, err := dockerWorkdir(cmd.Dir)
	if err != nil {
		return nil, err
	}
	timeout := cmd.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{"exec", "-w", workdir}
	for k, v := range cmd.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, sess.container, "sh", "-c", cmd.Command)

	p.setSessionStatus(sessionID, "running")
	out, runErr := p.runner.Run(execCtx, "docker", args, commandOptions{})
	p.setSessionStatus(sessionID, "ready")
	if execCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%w after %v", ErrCommandTimeout, timeout)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return nil, fmt.Errorf("docker exec: %w (stderr: %s)", runErr, out.Stderr)
		}
	}
	return &CommandResult{
		Stdout:   out.Stdout,
		Stderr:   out.Stderr,
		ExitCode: out.ExitCode,
		Duration: out.Duration,
	}, nil
}

// ReadFile reads a file from the Docker container.
func (p *DockerProvider) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	sess, err := p.session(sessionID)
	if err != nil {
		return nil, err
	}
	target, err := dockerWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	out, err := p.runner.Run(ctx, "docker", []string{"exec", sess.container, "sh", "-c", `cat -- "$1"`, "sh", target}, commandOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker read file %q: %w (stderr: %s)", path, err, out.Stderr)
	}
	return []byte(out.Stdout), nil
}

// WriteFile writes a file inside the Docker container.
func (p *DockerProvider) WriteFile(ctx context.Context, sessionID, path string, data []byte) error {
	sess, err := p.session(sessionID)
	if err != nil {
		return err
	}
	target, err := dockerWorkspacePath(path)
	if err != nil {
		return err
	}
	parent := pathDir(target)
	if out, err := p.runner.Run(ctx, "docker", []string{"exec", sess.container, "mkdir", "-p", parent}, commandOptions{}); err != nil {
		return fmt.Errorf("docker mkdir parent: %w (stderr: %s)", err, out.Stderr)
	}
	out, err := p.runner.Run(ctx, "docker", []string{"exec", "-i", sess.container, "sh", "-c", `cat > "$1"`, "sh", target}, commandOptions{
		Stdin: bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("docker write file %q: %w (stderr: %s)", path, err, out.Stderr)
	}
	return nil
}

// ApplyPatch applies a unified diff inside the Docker container.
func (p *DockerProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error {
	sess, err := p.session(sessionID)
	if err != nil {
		return err
	}
	out, err := p.runner.Run(ctx, "docker", []string{"exec", "-i", "-w", workspaceDir, sess.container, "git", "apply", "-"}, commandOptions{
		Stdin: strings.NewReader(patch),
	})
	if err != nil {
		return fmt.Errorf("docker git apply: %w (stderr: %s)", err, out.Stderr)
	}
	return nil
}

// Snapshot captures the current state of the Docker workspace as a git commit.
func (p *DockerProvider) Snapshot(ctx context.Context, sessionID string) (*Snapshot, error) {
	sess, err := p.session(sessionID)
	if err != nil {
		return nil, err
	}
	if out, err := p.runner.Run(ctx, "docker", []string{"exec", "-w", workspaceDir, sess.container, "git", "add", "-A"}, commandOptions{}); err != nil {
		return nil, fmt.Errorf("docker git add: %w (stderr: %s)", err, out.Stderr)
	}
	if out, err := p.runner.Run(ctx, "docker", []string{
		"exec", "-w", workspaceDir, sess.container,
		"git", "-c", "user.email=dev-plane@example.invalid", "-c", "user.name=Dev Plane",
		"commit", "--allow-empty", "-m", "snapshot: " + sessionID,
	}, commandOptions{}); err != nil {
		return nil, fmt.Errorf("docker git commit: %w (stderr: %s)", err, out.Stderr)
	}
	out, err := p.runner.Run(ctx, "docker", []string{"exec", "-w", workspaceDir, sess.container, "git", "rev-parse", "HEAD"}, commandOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker git rev-parse: %w (stderr: %s)", err, out.Stderr)
	}
	commit := strings.TrimSpace(out.Stdout)
	now := time.Now()
	return &Snapshot{
		ID:          commit,
		SessionID:   sessionID,
		GitCommit:   commit,
		Description: "Docker snapshot at " + now.Format(time.RFC3339),
		CreatedAt:   now,
	}, nil
}

// Restore restores a Docker workspace from a snapshot.
func (p *DockerProvider) Restore(ctx context.Context, sessionID string, snap *Snapshot) error {
	if snap == nil || snap.GitCommit == "" {
		return fmt.Errorf("snapshot git commit is required")
	}
	sess, err := p.session(sessionID)
	if err != nil {
		return err
	}
	out, err := p.runner.Run(ctx, "docker", []string{"exec", "-w", workspaceDir, sess.container, "git", "reset", "--hard", snap.GitCommit}, commandOptions{})
	if err != nil {
		return fmt.Errorf("docker git reset: %w (stderr: %s)", err, out.Stderr)
	}
	return nil
}

// GetStatus returns the current status of a Docker container session.
func (p *DockerProvider) GetStatus(ctx context.Context, sessionID string) (*SessionStatus, error) {
	sess, err := p.session(sessionID)
	if err != nil {
		return nil, err
	}
	out, err := p.runner.Run(ctx, "docker", []string{"inspect", "-f", "{{.State.Status}} {{.State.Pid}}", sess.container}, commandOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker inspect: %w (stderr: %s)", err, out.Stderr)
	}
	fields := strings.Fields(out.Stdout)
	status := sess.status
	pid := 0
	if len(fields) > 0 {
		status = dockerStatus(fields[0], sess.status)
	}
	if len(fields) > 1 {
		pid, _ = strconv.Atoi(fields[1])
	}
	return &SessionStatus{
		SessionID:  sessionID,
		Status:     status,
		PID:        pid,
		LastActive: sess.lastActive,
	}, nil
}

// StreamLogs returns a channel of log lines from the Docker container.
func (p *DockerProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan LogLine, error) {
	sess, err := p.session(sessionID)
	if err != nil {
		return nil, err
	}
	ch := make(chan LogLine, 100)
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	started, err := p.runner.Start(ctx, "docker", []string{"logs", "-f", sess.container}, commandOptions{
		Stdout: stdoutW,
		Stderr: stderrW,
	})
	if err != nil {
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
		return nil, fmt.Errorf("docker logs: %w", err)
	}
	go streamDockerLogs(ctx, ch, started, stdoutR, stdoutW, stderrR, stderrW)
	return ch, nil
}

func streamDockerLogs(ctx context.Context, ch chan<- LogLine, started startedCommand, stdoutR *io.PipeReader, stdoutW *io.PipeWriter, stderrR *io.PipeReader, stderrW *io.PipeWriter) {
	defer close(ch)
	var wg sync.WaitGroup
	scan := func(stream string, r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				return
			case ch <- LogLine{Timestamp: time.Now(), Stream: stream, Message: scanner.Text()}:
			}
		}
	}
	wg.Add(2)
	go scan("stdout", stdoutR)
	go scan("stderr", stderrR)
	_ = started.Wait()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	wg.Wait()
	_ = stdoutR.Close()
	_ = stderrR.Close()
}

// Close releases provider resources.
func (p *DockerProvider) Close() error {
	return nil
}

func (p *DockerProvider) session(sessionID string) (*dockerSession, error) {
	p.mu.RLock()
	sess, ok := p.sessions[sessionID]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	return sess, nil
}

func (p *DockerProvider) setSessionStatus(sessionID, status string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sess, ok := p.sessions[sessionID]; ok {
		sess.status = status
		sess.lastActive = time.Now()
	}
}

func dockerWorkdir(path string) (string, error) {
	if path == "" {
		return workspaceDir, nil
	}
	target, err := cleanRelativePath(path)
	if err != nil {
		return "", err
	}
	if target == "." {
		return workspaceDir, nil
	}
	return workspaceDir + "/" + filepath.ToSlash(target), nil
}

func dockerWorkspacePath(path string) (string, error) {
	target, err := cleanRelativePath(path)
	if err != nil {
		return "", err
	}
	if target == "." {
		return "", fmt.Errorf("file path is required")
	}
	return workspaceDir + "/" + filepath.ToSlash(target), nil
}

func cleanRelativePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", path)
	}
	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || strings.HasPrefix(filepath.ToSlash(clean), "../") {
		return "", fmt.Errorf("path traversal detected: %s", path)
	}
	return clean, nil
}

func pathDir(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx <= 0 {
		return workspaceDir
	}
	return path[:idx]
}

func dockerStatus(containerStatus, sessionStatus string) string {
	switch containerStatus {
	case "running":
		if sessionStatus == "running" {
			return "running"
		}
		return "ready"
	case "created":
		return "pending"
	case "paused":
		return "stopped"
	case "exited", "dead", "removing":
		return "stopped"
	default:
		return "error"
	}
}

func valueOrDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

var _ Provider = (*DockerProvider)(nil)
