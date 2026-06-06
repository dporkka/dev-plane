package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/audit"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
)

func TestExecuteToolUsesRuntimeProviderForDockerWorkspace(t *testing.T) {
	sessionID := "runtime-1"
	provider := &fakeRuntimeProvider{files: map[string][]byte{"README.md": []byte("# Runtime\n")}}
	runner := NewRunner(nil, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithRuntimeProvider("docker", provider)

	workspace := &models.Workspace{
		ID:               "ws-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
		Status:           models.WorkspaceStatusReady,
	}
	run := &models.AgentRun{ID: "run-1", AgentRole: models.AgentRoleImplementer}
	task := &models.Task{ID: "task-1", Title: "read docs"}

	output, err := runner.executeTool(context.Background(), run, task, workspace, "/missing-local-path", "read_file", json.RawMessage(`{"path":"README.md"}`))
	if err != nil {
		t.Fatalf("executeTool() error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if decoded["content"] != "# Runtime\n" {
		t.Fatalf("content = %q, want runtime file content", decoded["content"])
	}
	if provider.readSession != sessionID || provider.readPath != "README.md" {
		t.Fatalf("provider read = (%q, %q), want (%q, README.md)", provider.readSession, provider.readPath, sessionID)
	}
}

func TestRuntimeRunTestsDetectsCommandThroughProvider(t *testing.T) {
	sessionID := "runtime-1"
	provider := &fakeRuntimeProvider{
		files:         map[string][]byte{"go.mod": []byte("module example\n")},
		commandResult: &runtimes.CommandResult{Stdout: "ok  \texample\t0.1s\n", ExitCode: 0},
	}
	runner := NewRunner(nil, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithRuntimeProvider("docker", provider)

	workspace := &models.Workspace{
		ID:               "ws-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
		Status:           models.WorkspaceStatusReady,
	}
	run := &models.AgentRun{ID: "run-1", AgentRole: models.AgentRoleImplementer}
	task := &models.Task{ID: "task-1", Title: "test"}

	output, err := runner.executeTool(context.Background(), run, task, workspace, "", "run_tests", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("executeTool() error: %v", err)
	}
	if len(provider.commands) != 1 || provider.commands[0].Command != "go test ./..." {
		t.Fatalf("commands = %#v, want go test ./...", provider.commands)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if decoded["passed"] != true {
		t.Fatalf("passed = %v, want true", decoded["passed"])
	}
}

func TestAgentToolPersistsCapabilityAudit(t *testing.T) {
	db := setupAgentAuditDB(t)
	defer db.Close()

	workspacePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspacePath, "README.md"), []byte("# Audit\n"), 0644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	logger := slog.Default()
	runner := NewRunner(db, tools.NewWorkspaceTools(logger), allowAllPolicies(), nil, nil, logger).
		WithCapabilityKernel(capability.NewKernel(allowAllPolicies(), nil, audit.NewLogger(db, logger), logger))
	run := &models.AgentRun{ID: "33333333-3333-3333-3333-333333333333", AgentRole: models.AgentRoleImplementer}
	task := &models.Task{ID: "task-1", ProjectID: "project-1", Title: "audit tool"}
	workspace := &models.Workspace{ID: "workspace-1", RuntimeProvider: "local", Status: models.WorkspaceStatusReady}

	if _, err := runner.executeTool(context.Background(), run, task, workspace, workspacePath, "read_file", json.RawMessage(`{"path":"README.md"}`)); err != nil {
		t.Fatalf("executeTool() error: %v", err)
	}

	var actorType, actorID, orgID string
	if err := db.QueryRow(`
		SELECT actor_type, actor_id, organization_id
		FROM audit_logs
		WHERE action = 'capability_check'
	`).Scan(&actorType, &actorID, &orgID); err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if actorType != "agent" {
		t.Fatalf("actor_type = %q, want agent", actorType)
	}
	if actorID != run.ID {
		t.Fatalf("actor_id = %q, want run id", actorID)
	}
	if orgID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("organization_id = %q", orgID)
	}
}

func allowAllPolicies() *policies.Engine {
	return policies.NewEngine([]policies.Policy{
		{Name: "allow_all_tests", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	})
}

type fakeRuntimeProvider struct {
	files         map[string][]byte
	readSession   string
	readPath      string
	commands      []runtimes.Command
	commandResult *runtimes.CommandResult
}

func setupAgentAuditDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL
		);
		INSERT INTO projects (id, organization_id)
		VALUES ('project-1', '22222222-2222-2222-2222-222222222222');
		CREATE TABLE audit_logs (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_id TEXT,
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT,
			details TEXT,
			created_at DATETIME
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create audit test schema: %v", err)
	}
	return db
}

func (p *fakeRuntimeProvider) CreateWorkspace(ctx context.Context, req runtimes.CreateRequest) (*runtimes.Session, error) {
	return nil, nil
}

func (p *fakeRuntimeProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	return nil
}

func (p *fakeRuntimeProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	p.commands = append(p.commands, cmd)
	if p.commandResult != nil {
		return p.commandResult, nil
	}
	return &runtimes.CommandResult{}, nil
}

func (p *fakeRuntimeProvider) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	p.readSession = sessionID
	p.readPath = path
	data, ok := p.files[path]
	if !ok {
		return nil, runtimes.ErrSessionNotFound
	}
	return data, nil
}

func (p *fakeRuntimeProvider) WriteFile(ctx context.Context, sessionID, path string, data []byte) error {
	if p.files == nil {
		p.files = map[string][]byte{}
	}
	p.files[path] = append([]byte(nil), data...)
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
	return &runtimes.SessionStatus{SessionID: sessionID, Status: "ready"}, nil
}

func (p *fakeRuntimeProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan runtimes.LogLine, error) {
	ch := make(chan runtimes.LogLine)
	close(ch)
	return ch, nil
}
