package vcs

import "context"

// Backend is the machine-facing source-control interface used by agent workers.
// Implementations should avoid exposing raw shell commands to agents.
type Backend interface {
	Name() string
	CloneOrFetch(ctx context.Context, req CloneRequest) error
	CreateWorkspace(ctx context.Context, req WorkspaceRequest) error
	RemoveWorkspace(ctx context.Context, req WorkspaceRequest) error
	Status(ctx context.Context, workspacePath string) (string, error)
	Diff(ctx context.Context, workspacePath string) (string, error)
	Snapshot(ctx context.Context, workspacePath, message string) (Revision, error)
	Restore(ctx context.Context, workspacePath, revision string) error
	Publish(ctx context.Context, req PublishRequest) error
}

// CloneRequest describes a local repository cache. URL must not contain
// embedded credentials; provide authentication through Env instead.
type CloneRequest struct {
	URL  string
	Path string
	Env  map[string]string
}

// WorkspaceRequest describes an isolated working copy for one agent task.
// Base should be explicit (for example main, main@origin, or a commit ID).
type WorkspaceRequest struct {
	RepositoryPath string
	WorkspacePath  string
	Name           string
	Base           string
	// Ref is the forge-visible Git branch/bookmark to publish. It may differ
	// from the local workspace name.
	Ref            string
}

type PublishRequest struct {
	WorkspacePath string
	Ref           string
	Env           map[string]string
}

// Revision identifies the immutable Git commit and, when available, the
// evolution-stable VCS change identifier.
type Revision struct {
	CommitID string `json:"commit_id"`
	ChangeID string `json:"change_id,omitempty"`
}

// Workspace binds an agent task to one isolated source-control workspace.
type Workspace struct {
	TaskID         string            `json:"task_id"`
	AgentID        string            `json:"agent_id"`
	RepositoryPath string            `json:"repository_path"`
	WorkspacePath  string            `json:"workspace_path"`
	Name           string            `json:"name"`
	Base           string            `json:"base"`
	PublishRef     string            `json:"publish_ref"`
	AuthEnv        map[string]string `json:"-"`
}
