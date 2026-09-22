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
	WorkspacePath string
	Ref           string
	Env           map[string]string
}

// ArtifactRevision identifies the immutable artifact state associated with a
// source-control snapshot. Digests use the artifact store's algorithm:hex form.
type ArtifactRevision struct {
	ManifestDigest string `json:"manifest_digest,omitempty"`
	VersionDigest  string `json:"version_digest,omitempty"`
}

// Revision identifies the immutable Git commit and, when available, the
// evolution-stable VCS change identifier plus the non-code artifact state that
// was captured at the same logical snapshot.
type Revision struct {
	CommitID               string `json:"commit_id"`
	ChangeID               string `json:"change_id,omitempty"`
	ArtifactManifestDigest string `json:"artifact_manifest_digest,omitempty"`
	ArtifactVersionDigest  string `json:"artifact_version_digest,omitempty"`
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
