package runtimes

import (
	"context"
	"errors"
	"time"
)

// ErrSessionNotFound is returned when a session does not exist.
var ErrSessionNotFound = errors.New("session not found")

// ErrSessionNotReady is returned when a session is not in a ready state.
var ErrSessionNotReady = errors.New("session not ready")

// ErrCommandTimeout is returned when a command exceeds its timeout.
var ErrCommandTimeout = errors.New("command timed out")

// ErrNotImplemented is returned by Phase 1 stubs.
var ErrNotImplemented = errors.New("not implemented in Phase 1")

// Provider defines the interface for workspace runtimes.
// Implementations manage the lifecycle of isolated environments where
// agents execute commands and manipulate files.
type Provider interface {
	// CreateWorkspace provisions a new workspace session from the given request.
	CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error)

	// DestroyWorkspace tears down a workspace session and cleans up resources.
	DestroyWorkspace(ctx context.Context, sessionID string) error

	// ExecuteCommand runs a command within the workspace session.
	ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error)

	// ReadFile reads a file from the workspace at the given path.
	ReadFile(ctx context.Context, sessionID, path string) ([]byte, error)

	// WriteFile writes data to a file in the workspace at the given path.
	WriteFile(ctx context.Context, sessionID, path string, data []byte) error

	// ApplyPatch applies a unified diff patch within the workspace.
	ApplyPatch(ctx context.Context, sessionID, patch string) error

	// Snapshot captures the current state of the workspace.
	Snapshot(ctx context.Context, sessionID string) (*Snapshot, error)

	// Restore restores a workspace from a snapshot.
	Restore(ctx context.Context, sessionID string, snap *Snapshot) error

	// GetStatus returns the current status of a workspace session.
	GetStatus(ctx context.Context, sessionID string) (*SessionStatus, error)

	// StreamLogs returns a channel of log lines from the workspace session.
	StreamLogs(ctx context.Context, sessionID string) (<-chan LogLine, error)
}

// CreateRequest contains parameters for creating a new workspace session.
type CreateRequest struct {
	RepositoryID string            `json:"repository_id"`
	CloneURL     string            `json:"clone_url"`
	Branch       string            `json:"branch"`
	BaseBranch   string            `json:"base_branch"`
	WorktreeName string            `json:"worktree_name"`
	Env          map[string]string `json:"env,omitempty"`
}

// Session represents an active workspace runtime session.
type Session struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	Status       string    `json:"status"` // pending, ready, running, stopped, error, destroyed
	PreviewURL   string    `json:"preview_url,omitempty"`
	Provider     string    `json:"provider"`
	WorktreePath string    `json:"worktree_path,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// Command represents a command to execute in a workspace.
type Command struct {
	Command string            `json:"command"`
	Dir     string            `json:"dir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Timeout time.Duration     `json:"timeout,omitempty"`
}

// CommandResult contains the output of an executed command.
type CommandResult struct {
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration_ms"`
}

// SessionStatus provides detailed status information about a session.
type SessionStatus struct {
	SessionID   string    `json:"session_id"`
	Status      string    `json:"status"`
	PID         int       `json:"pid,omitempty"`
	CPUUsage    float64   `json:"cpu_usage,omitempty"`
	MemoryUsage int64     `json:"memory_usage_bytes,omitempty"`
	DiskUsage   int64     `json:"disk_usage_bytes,omitempty"`
	LastActive  time.Time `json:"last_active"`
}

// LogLine represents a single log entry from a workspace session.
type LogLine struct {
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream"` // stdout, stderr, event
	Message   string    `json:"message"`
}

// Snapshot captures a point-in-time state of a workspace.
type Snapshot struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	GitCommit   string    `json:"git_commit,omitempty"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}
