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
}

type PublishRequest struct {
	WorkspacePath string `json:"workspace_path,omitempty"`
	Ref           string `json:"ref"`
	// SourceRevision pins publication to an immutable reviewed revision. When
	// empty, backends preserve the legacy behavior of publishing Ref/working-copy state.
	SourceRevision Revision `json:"source_revision,omitempty"`
	// RemoteURL, when set, is used instead of the repository's configured
	// origin. Privileged publishers should set it from trusted repository
	// metadata so agent-controlled Git config cannot redirect credentials.
	RemoteURL string            `json:"remote_url,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
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
