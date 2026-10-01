package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/runtimes"
)

type fakeRuntimeProvider struct {
	req       runtimes.CreateRequest
	baseSHA   string
	destroyed []string
}

func (p *fakeRuntimeProvider) CreateWorkspace(ctx context.Context, req runtimes.CreateRequest) (*runtimes.Session, error) {
	p.req = req
	return &runtimes.Session{
		ID:           "runtime-session-1",
		WorkspaceID:  req.RepositoryID,
		Status:       "ready",
		Provider:     "local",
		WorktreePath: "/tmp/workspace",
		CreatedAt:    time.Now(),
	}, nil
}

func (p *fakeRuntimeProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	p.destroyed = append(p.destroyed, sessionID)
	return nil
}
func (p *fakeRuntimeProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	if reflect.DeepEqual(cmd.Args, []string{"git", "rev-parse", "HEAD"}) {
		sha := p.baseSHA
		if sha == "" {
			sha = "base-sha-123"
		}
		return &runtimes.CommandResult{Stdout: sha + "\n", ExitCode: 0}, nil
	}
	return nil, nil
}
func (p *fakeRuntimeProvider) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	return nil, nil
}
func (p *fakeRuntimeProvider) WriteFile(ctx context.Context, sessionID, path string, data []byte) error {
	return nil
}
func (p *fakeRuntimeProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error {
	return nil
}
func (p *fakeRuntimeProvider) Snapshot(ctx context.Context, sessionID string) (*runtimes.Snapshot, error) {
	return nil, nil
}
func (p *fakeRuntimeProvider) Restore(ctx context.Context, sessionID string, snap *runtimes.Snapshot) error {
	return nil
}
func (p *fakeRuntimeProvider) GetStatus(ctx context.Context, sessionID string) (*runtimes.SessionStatus, error) {
	return nil, nil
}
func (p *fakeRuntimeProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan runtimes.LogLine, error) {
	ch := make(chan runtimes.LogLine)
	close(ch)
	return ch, nil
}

func TestProvisionWorkspaceUsesRuntimeProvider(t *testing.T) {
	provider := &fakeRuntimeProvider{}
	handler := NewTaskHandler(nil, slog.Default()).WithRuntimeProvider(provider, "local")
	now := time.Unix(1234, 0).UTC()

	workspace, err := handler.provisionWorkspace(context.Background(), approvedTask{
		ID:            "task-123456789",
		RepositoryID:  "repo-1",
		TargetBranch:  "main",
		CloneURL:      "https://example.invalid/repo.git",
		DefaultBranch: "trunk",
		WorkspaceID:   "workspace-1",
	}, now)
	if err != nil {
		t.Fatalf("provisionWorkspace() error: %v", err)
	}

	if workspace.Status != "ready" {
		t.Fatalf("Status = %q, want ready", workspace.Status)
	}
	if workspace.RuntimeSessionID == nil || *workspace.RuntimeSessionID != "runtime-session-1" {
		t.Fatalf("RuntimeSessionID = %v, want runtime-session-1", workspace.RuntimeSessionID)
	}
	if workspace.WorktreePath == nil || *workspace.WorktreePath != "/tmp/workspace" {
		t.Fatalf("WorktreePath = %v, want /tmp/workspace", workspace.WorktreePath)
	}
	if provider.req.CloneURL != "https://example.invalid/repo.git" {
		t.Fatalf("CloneURL = %q", provider.req.CloneURL)
	}
	if provider.req.Branch != "agent/task-123/1234" {
		t.Fatalf("Branch = %q, want agent/task-123/1234", provider.req.Branch)
	}
	if provider.req.BaseBranch != "main" {
		t.Fatalf("BaseBranch = %q, want main", provider.req.BaseBranch)
	}
	if provider.req.WorktreeName != "workspace-task-123" {
		t.Fatalf("WorktreeName = %q, want workspace-task-123", provider.req.WorktreeName)
	}
}

func TestProvisionWorkspaceWithoutRuntimeProviderStaysPending(t *testing.T) {
	handler := NewTaskHandler(nil, slog.Default())

	workspace, err := handler.provisionWorkspace(context.Background(), approvedTask{
		ID:            "short",
		RepositoryID:  "repo-1",
		CloneURL:      "https://example.invalid/repo.git",
		DefaultBranch: "trunk",
	}, time.Unix(1234, 0).UTC())
	if err != nil {
		t.Fatalf("provisionWorkspace() error: %v", err)
	}
	if workspace.Status != "pending" {
		t.Fatalf("Status = %q, want pending", workspace.Status)
	}
	if workspace.RuntimeProvider != "unprovisioned" {
		t.Fatalf("RuntimeProvider = %q, want unprovisioned", workspace.RuntimeProvider)
	}
	if workspace.BranchName != "agent/short/1234" {
		t.Fatalf("BranchName = %q, want agent/short/1234", workspace.BranchName)
	}
	if workspace.BaseBranch != "trunk" {
		t.Fatalf("BaseBranch = %q, want trunk", workspace.BaseBranch)
	}
}

func TestHandleTaskApprovedCreatesWorkspaceRunAndPublishesRunTriggered(t *testing.T) {
	db := setupTaskHandlerDB(t)
	defer db.Close()
	insertApprovedTaskFixture(t, db)
	provider := &fakeRuntimeProvider{baseSHA: "abc123def456"}
	publisher := &fakeWorkerEventPublisher{}
	handler := NewTaskHandler(db, slog.Default()).
		WithEventPublisher(publisher).
		WithRuntimeProvider(provider, "local")

	err := handler.HandleTaskApproved(&nats.Msg{Data: []byte(`{"task_id":"task-1","status":"approved","data":{"admission":{"policy":"task-readiness-v1","readiness":{"status":"ready","checks":[]}}}}`)})
	if err != nil {
		t.Fatalf("HandleTaskApproved() error: %v", err)
	}

	var taskStatus, workspaceID string
	if err := db.QueryRow(`SELECT status, workspace_id FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus, &workspaceID); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if taskStatus != "running" || workspaceID == "" {
		t.Fatalf("task status/workspace = %q/%q, want running/non-empty", taskStatus, workspaceID)
	}

	var runID, runStatus, runMetadata string
	if err := db.QueryRow(`SELECT id, status, metadata FROM agent_runs WHERE task_id = 'task-1'`).Scan(&runID, &runStatus, &runMetadata); err != nil {
		t.Fatalf("query run: %v", err)
	}
	if runID == "" || runStatus != "queued" {
		t.Fatalf("run id/status = %q/%q, want non-empty/queued", runID, runStatus)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(runMetadata), &metadata); err != nil {
		t.Fatalf("unmarshal run metadata: %v", err)
	}
	admission, ok := metadata["admission"].(map[string]any)
	if !ok || admission["policy"] != "task-readiness-v1" {
		t.Fatalf("run metadata admission = %+v", metadata["admission"])
	}
	if publisher.subject != events.RunTriggered {
		t.Fatalf("published subject = %q, want %s", publisher.subject, events.RunTriggered)
	}
	var payload map[string]any
	if err := json.Unmarshal(publisher.data, &payload); err != nil {
		t.Fatalf("unmarshal run triggered payload: %v", err)
	}
	if payload["run_id"] != runID || payload["task_id"] != "task-1" || payload["action"] != "task_approved" {
		t.Fatalf("payload = %+v", payload)
	}
	if provider.req.CloneURL != "https://example.invalid/repo.git" {
		t.Fatalf("runtime clone URL = %q", provider.req.CloneURL)
	}
	if provider.req.IdempotencyKey != "workspace:"+runID {
		t.Fatalf("runtime idempotency key = %q, want workspace:%s", provider.req.IdempotencyKey, runID)
	}
	if provider.req.Metadata["dev_plane_task_id"] != "task-1" || provider.req.Metadata["dev_plane_run_id"] != runID {
		t.Fatalf("runtime metadata = %+v", provider.req.Metadata)
	}
	if provider.req.Limits.CPUMillis <= 0 || provider.req.Limits.MemoryMB <= 0 || provider.req.Limits.DiskMB <= 0 || provider.req.Limits.WallTimeSeconds <= 0 {
		t.Fatalf("runtime limits must be explicit: %+v", provider.req.Limits)
	}
	if provider.req.Capabilities.Network {
		t.Fatal("runtime network capability must default deny")
	}
	rawManifest, ok := metadata["run_manifest"]
	if !ok {
		t.Fatalf("run metadata missing run_manifest: %+v", metadata)
	}
	manifestBytes, err := json.Marshal(rawManifest)
	if err != nil {
		t.Fatalf("marshal run manifest: %v", err)
	}
	var manifest models.RunManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("unmarshal run manifest: %v", err)
	}
	if manifest.RunID != runID || manifest.TaskID != "task-1" || manifest.BaseSHA != "abc123def456" {
		t.Fatalf("run manifest identity = %+v", manifest)
	}
	if err := manifest.VerifyDigest(); err != nil {
		t.Fatalf("run manifest digest invalid: %v", err)
	}
	if metadata["run_manifest_digest"] != manifest.Digest {
		t.Fatalf("run_manifest_digest = %v, want %s", metadata["run_manifest_digest"], manifest.Digest)
	}
}

func TestHandleTaskApprovedRejectsMalformedAdmissionBeforeSideEffects(t *testing.T) {
	db := setupTaskHandlerDB(t)
	defer db.Close()
	insertApprovedTaskFixture(t, db)
	provider := &fakeRuntimeProvider{}
	handler := NewTaskHandler(db, slog.Default()).
		WithRuntimeProvider(provider, "local")

	err := handler.HandleTaskApproved(&nats.Msg{Data: []byte(`{"task_id":"task-1","status":"approved","data":[]}`)})
	if err == nil {
		t.Fatal("HandleTaskApproved() error = nil, want malformed metadata error")
	}
	if provider.req.CloneURL != "" {
		t.Fatalf("runtime provider called before metadata validation: %+v", provider.req)
	}
	var taskStatus string
	var workspaceID sql.NullString
	if err := db.QueryRow(`SELECT status, workspace_id FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus, &workspaceID); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if taskStatus != "approved" || workspaceID.Valid {
		t.Fatalf("task status/workspace = %q/%v, want approved/null", taskStatus, workspaceID)
	}
	var workspaceCount, runCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&workspaceCount); err != nil {
		t.Fatalf("query workspace count: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&runCount); err != nil {
		t.Fatalf("query run count: %v", err)
	}
	if workspaceCount != 0 || runCount != 0 {
		t.Fatalf("workspace/run counts = %d/%d, want 0/0", workspaceCount, runCount)
	}
}

func TestHandleTaskApprovedSkipsWhenInitialRunAlreadyClaimed(t *testing.T) {
	db := setupTaskHandlerDB(t)
	defer db.Close()
	insertApprovedTaskFixture(t, db)
	if _, err := db.Exec(`UPDATE tasks SET status = 'running' WHERE id = 'task-1'`); err != nil {
		t.Fatalf("mark task running: %v", err)
	}
	provider := &fakeRuntimeProvider{}
	publisher := &fakeWorkerEventPublisher{}
	handler := NewTaskHandler(db, slog.Default()).
		WithEventPublisher(publisher).
		WithRuntimeProvider(provider, "local")

	err := handler.HandleTaskApproved(&nats.Msg{Data: []byte(`{"task_id":"task-1","status":"approved","data":{"admission":{"policy":"task-readiness-v1","readiness":{"status":"ready","checks":[]}}}}`)})
	if err != nil {
		t.Fatalf("HandleTaskApproved() error: %v", err)
	}
	if provider.req.CloneURL != "" {
		t.Fatalf("runtime provider called for already-claimed task: %+v", provider.req)
	}
	var workspaceCount, runCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&workspaceCount); err != nil {
		t.Fatalf("query workspace count: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&runCount); err != nil {
		t.Fatalf("query run count: %v", err)
	}
	if workspaceCount != 0 || runCount != 0 {
		t.Fatalf("workspace/run counts = %d/%d, want 0/0", workspaceCount, runCount)
	}
	if publisher.subject != "" {
		t.Fatalf("unexpected event published: %q", publisher.subject)
	}
}

func TestHandleTaskApprovedDoesNotRepublishFollowOnRun(t *testing.T) {
	db := setupTaskHandlerDB(t)
	defer db.Close()
	insertApprovedTaskFixture(t, db)
	if _, err := db.Exec(`UPDATE tasks SET status = 'running' WHERE id = 'task-1'`); err != nil {
		t.Fatalf("mark task running: %v", err)
	}
	_, err := db.Exec(`
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, total_cost, metadata, created_at, updated_at
		) VALUES (
			'run-follow-on', 'task-1', NULL, 'reviewer', 'gpt-4o', 'openai', 'queued', 0,
			'{"admission":{"policy":"task-readiness-v1","readiness":{"status":"ready","checks":[]}},"trigger":"mailbox_handoff","handoff_from_run_id":"run-1"}',
			CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("insert follow-on run: %v", err)
	}
	publisher := &fakeWorkerEventPublisher{}
	provider := &fakeRuntimeProvider{}
	handler := NewTaskHandler(db, slog.Default()).
		WithEventPublisher(publisher).
		WithRuntimeProvider(provider, "local")

	err = handler.HandleTaskApproved(&nats.Msg{Data: []byte(`{"task_id":"task-1","status":"approved","data":{"admission":{"policy":"task-readiness-v1","readiness":{"status":"ready","checks":[]}}}}`)})
	if err != nil {
		t.Fatalf("HandleTaskApproved() error: %v", err)
	}
	if publisher.subject != "" {
		t.Fatalf("published follow-on run as approval recovery: %q %s", publisher.subject, string(publisher.data))
	}
	if provider.req.CloneURL != "" {
		t.Fatalf("runtime provider called for already-running task: %+v", provider.req)
	}
}

func TestHandleTaskApprovedRepublishesExistingQueuedRun(t *testing.T) {
	db := setupTaskHandlerDB(t)
	defer db.Close()
	insertApprovedTaskFixture(t, db)
	_, err := db.Exec(`
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, total_cost, metadata, created_at, updated_at
		) VALUES (
			'run-existing', 'task-1', NULL, 'implementer', 'gpt-4o', 'openai', 'queued', 0, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		t.Fatalf("insert queued run: %v", err)
	}
	publisher := &fakeWorkerEventPublisher{}
	provider := &fakeRuntimeProvider{}
	handler := NewTaskHandler(db, slog.Default()).
		WithEventPublisher(publisher).
		WithRuntimeProvider(provider, "local")

	err = handler.HandleTaskApproved(&nats.Msg{Data: []byte(`{"task_id":"task-1","status":"approved"}`)})
	if err != nil {
		t.Fatalf("HandleTaskApproved() error: %v", err)
	}

	var runCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE task_id = 'task-1'`).Scan(&runCount); err != nil {
		t.Fatalf("query run count: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("run count = %d, want 1", runCount)
	}
	if provider.req.CloneURL != "" {
		t.Fatalf("runtime provider was called on retry: %+v", provider.req)
	}
	if publisher.subject != events.RunTriggered || !strings.Contains(string(publisher.data), "run-existing") {
		t.Fatalf("published %q %s, want run-existing trigger", publisher.subject, string(publisher.data))
	}
}

func setupTaskHandlerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE repositories (
			id TEXT PRIMARY KEY,
			clone_url TEXT NOT NULL,
			default_branch TEXT NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			target_branch TEXT NOT NULL,
			workspace_id TEXT,
			status TEXT NOT NULL,
			started_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			task_id TEXT,
			name TEXT NOT NULL,
			branch TEXT NOT NULL,
			base_branch TEXT NOT NULL,
			worktree_path TEXT,
			runtime_provider TEXT NOT NULL,
			runtime_session_id TEXT,
			status TEXT NOT NULL,
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_role TEXT NOT NULL,
			model TEXT,
			provider TEXT,
			status TEXT NOT NULL,
			total_cost REAL DEFAULT 0,
			metadata TEXT DEFAULT '{}',
			created_at DATETIME,
			updated_at DATETIME
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func insertApprovedTaskFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO repositories (id, clone_url, default_branch)
		VALUES ('repo-1', 'https://example.invalid/repo.git', 'main');
		INSERT INTO tasks (id, repository_id, target_branch, status)
		VALUES ('task-1', 'repo-1', 'main', 'approved');
	`)
	if err != nil {
		t.Fatalf("insert approved task fixture: %v", err)
	}
}


func TestHandleTaskApprovedCleansUpWorkspaceWhenBaseRevisionCannotBeResolved(t *testing.T) {
	db := setupTaskHandlerDB(t)
	defer db.Close()
	insertApprovedTaskFixture(t, db)

	provider := &fakeRuntimeProvider{baseSHA: "   "}
	handler := NewTaskHandler(db, slog.Default()).WithRuntimeProvider(provider, "local")

	err := handler.HandleTaskApproved(&nats.Msg{Data: []byte(`{"task_id":"task-1","status":"approved","data":{"admission":{"policy":"task-readiness-v1"}}}`)})
	if err == nil || !strings.Contains(err.Error(), "base revision") {
		t.Fatalf("HandleTaskApproved() error = %v, want base revision error", err)
	}
	if len(provider.destroyed) != 1 || provider.destroyed[0] != "runtime-session-1" {
		t.Fatalf("destroyed sessions = %v, want runtime-session-1", provider.destroyed)
	}
	var status string
	var workspaceID sql.NullString
	if err := db.QueryRow(`SELECT status, workspace_id FROM tasks WHERE id = 'task-1'`).Scan(&status, &workspaceID); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if status != "approved" || workspaceID.Valid {
		t.Fatalf("task status/workspace = %q/%v, want approved/null", status, workspaceID)
	}
}
