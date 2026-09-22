package handlers

import (
	"context"
	"database/sql"
	"testing"
	"time"

	artifactstore "github.com/ai-dev-control-plane/artifacts"
	"github.com/ai-dev-control-plane/runtimes"
	_ "github.com/mattn/go-sqlite3"
)

func TestArtifactSnapshotterCaptureIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_role TEXT NOT NULL
		);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			runtime_session_id TEXT,
			worktree_path TEXT
		);
		CREATE TABLE artifacts (
			id TEXT PRIMARY KEY,
			workspace_id TEXT,
			logical_path TEXT,
			is_tombstone BOOLEAN NOT NULL DEFAULT false,
			artifact_json TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE workspace_snapshots (
			id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			agent_run_id TEXT UNIQUE,
			git_commit TEXT,
			vcs_change_id TEXT,
			artifact_manifest_digest TEXT,
			artifact_version_digest TEXT,
			description TEXT,
			metadata TEXT,
			created_at DATETIME NOT NULL
		);
		INSERT INTO workspaces (id, runtime_session_id) VALUES ('workspace-1', 'session-1');
		INSERT INTO agent_runs (id, task_id, workspace_id, agent_role)
			VALUES ('run-1', 'task-1', 'workspace-1', 'implementer');
	`)
	if err != nil {
		t.Fatal(err)
	}

	store, err := artifactstore.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := artifactstore.NewManager(store)
	if err != nil {
		t.Fatal(err)
	}
	provider := &snapshotRuntimeProvider{
		snapshot: &runtimes.Snapshot{
			ID:        "commit-1",
			SessionID: "session-1",
			GitCommit: "commit-1",
			CreatedAt: time.Now().UTC(),
		},
	}
	snapshotter := NewArtifactSnapshotter(db, manager, provider)

	first, err := snapshotter.Capture(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshotter.Capture(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("snapshot ids differ: %s vs %s", first.ID, second.ID)
	}
	if first.GitCommit == nil || *first.GitCommit != "commit-1" {
		t.Fatalf("git commit = %v, want commit-1", first.GitCommit)
	}
	if first.ArtifactVersionDigest == nil || first.ArtifactManifestDigest == nil {
		t.Fatalf("empty artifact tree should still produce immutable version + manifest: %+v", first)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_snapshots WHERE agent_run_id = 'run-1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("snapshot count = %d, want 1", count)
	}

	versionDigest, err := artifactstore.ParseDigest(*first.ArtifactVersionDigest)
	if err != nil {
		t.Fatal(err)
	}
	version, err := manager.LoadVersion(context.Background(), versionDigest)
	if err != nil {
		t.Fatal(err)
	}
	if version.Provenance.TaskID != "task-1" ||
		version.Provenance.AgentID != "run-1" ||
		version.Provenance.WorkspaceID != "workspace-1" ||
		version.Provenance.SourceRevision != "commit-1" {
		t.Fatalf("unexpected version provenance: %+v", version.Provenance)
	}
}

type snapshotRuntimeProvider struct {
	snapshot *runtimes.Snapshot
}

func (p *snapshotRuntimeProvider) CreateWorkspace(context.Context, runtimes.CreateRequest) (*runtimes.Session, error) {
	return nil, nil
}
func (p *snapshotRuntimeProvider) DestroyWorkspace(context.Context, string) error { return nil }
func (p *snapshotRuntimeProvider) ExecuteCommand(context.Context, string, runtimes.Command) (*runtimes.CommandResult, error) {
	return nil, nil
}
func (p *snapshotRuntimeProvider) ReadFile(context.Context, string, string) ([]byte, error) { return nil, nil }
func (p *snapshotRuntimeProvider) WriteFile(context.Context, string, string, []byte) error { return nil }
func (p *snapshotRuntimeProvider) ApplyPatch(context.Context, string, string) error { return nil }
func (p *snapshotRuntimeProvider) Snapshot(context.Context, string) (*runtimes.Snapshot, error) {
	copy := *p.snapshot
	return &copy, nil
}
func (p *snapshotRuntimeProvider) Restore(context.Context, string, *runtimes.Snapshot) error { return nil }
func (p *snapshotRuntimeProvider) GetStatus(context.Context, string) (*runtimes.SessionStatus, error) {
	return &runtimes.SessionStatus{Status: "ready"}, nil
}
func (p *snapshotRuntimeProvider) StreamLogs(context.Context, string) (<-chan runtimes.LogLine, error) {
	ch := make(chan runtimes.LogLine)
	close(ch)
	return ch, nil
}
