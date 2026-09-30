package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/modelrouter"
	"github.com/ai-dev-control-plane/api/internal/tools"
	runfailure "github.com/ai-dev-control-plane/failure"
	"github.com/ai-dev-control-plane/models"
)

func TestRunPersistsTestFailureClassification(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()
	createFailureProjectConfigTable(t, db, "false")

	workspacePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspacePath, "README.md"), []byte("# Project\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)
	if _, err := db.Exec(`UPDATE agent_runs SET metadata = '{"trace_id":"trace-1"}' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("seed run metadata: %v", err)
	}

	provider := &fakeModelProvider{responses: []string{
		`{"action":"final_response","content":"Implementation complete."}`,
	}}
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))

	if err := runner.Run(context.Background(), "run-1"); err == nil {
		t.Fatal("Run() error = nil, want final verification failure")
	}

	var status, metadata string
	if err := db.QueryRow(`SELECT status, metadata FROM agent_runs WHERE id = 'run-1'`).Scan(&status, &metadata); err != nil {
		t.Fatalf("query failed run: %v", err)
	}
	if status != models.AgentRunStatusFailed {
		t.Fatalf("status = %q, want failed", status)
	}
	assertFailureMetadata(t, metadata, failureMetadataExpectation{
		Category:    "test",
		Retryable:   false,
		Disposition: "fix",
		Stage:       "verification",
		Source:      "tests",
	})
}

func TestRunPersistsRetryableInfrastructureFailureClassification(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()
	createFailureProjectConfigTable(t, db, "printf 'connection reset by peer\\n' >&2; exit 1")

	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)

	provider := &fakeModelProvider{responses: []string{
		`{"action":"final_response","content":"Implementation complete."}`,
	}}
	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default()).
		WithModelRouter(modelrouter.NewRouter(testRouterConfig(), provider))

	if err := runner.Run(context.Background(), "run-1"); err == nil {
		t.Fatal("Run() error = nil, want final verification failure")
	}

	var metadata string
	if err := db.QueryRow(`SELECT metadata FROM agent_runs WHERE id = 'run-1'`).Scan(&metadata); err != nil {
		t.Fatalf("query failed run metadata: %v", err)
	}
	assertFailureMetadata(t, metadata, failureMetadataExpectation{
		Category:    "infrastructure",
		Retryable:   true,
		Disposition: "retry",
		Stage:       "verification",
		Source:      "tests",
	})
}

type failureMetadataExpectation struct {
	Category    string
	Retryable   bool
	Disposition string
	Stage       string
	Source      string
}

func assertFailureMetadata(t *testing.T, raw string, want failureMetadataExpectation) {
	t.Helper()
	var metadata struct {
		TraceID string `json:"trace_id"`
		Failure struct {
			Category    string `json:"category"`
			Retryable   bool   `json:"retryable"`
			Disposition string `json:"disposition"`
			Stage       string `json:"stage"`
			Source      string `json:"source"`
		} `json:"failure"`
	}
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata.Failure.Category != want.Category ||
		metadata.Failure.Retryable != want.Retryable ||
		metadata.Failure.Disposition != want.Disposition ||
		metadata.Failure.Stage != want.Stage ||
		metadata.Failure.Source != want.Source {
		t.Fatalf("failure metadata = %+v, want %+v", metadata.Failure, want)
	}
	if want.Category == "test" && metadata.TraceID != "trace-1" {
		t.Fatalf("trace_id = %q, want preserved trace-1", metadata.TraceID)
	}
}

func createFailureProjectConfigTable(t *testing.T, db interface {
	Exec(query string, args ...any) (sql.Result, error)
}, testCommand string) {
	t.Helper()
	_, err := db.Exec(`
		CREATE TABLE project_configs (
			id TEXT PRIMARY KEY,
			repository_id TEXT NOT NULL,
			test_command TEXT,
			lint_command TEXT,
			typecheck_command TEXT,
			build_command TEXT,
			updated_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("create project_configs: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO project_configs (
			id, repository_id, test_command, lint_command, typecheck_command, build_command, updated_at
		) VALUES ('config-1', 'repo-1', $1, '', '', '', CURRENT_TIMESTAMP)
	`, testCommand); err != nil {
		t.Fatalf("insert project config: %v", err)
	}
}


func TestBuildFailedRunEventUsesCanonicalEnvelope(t *testing.T) {
	db := setupRunnerOrchestrationDB(t)
	defer db.Close()
	workspacePath := t.TempDir()
	insertRunnerFixture(t, db, workspacePath, models.AgentRoleImplementer)

	runner := NewRunner(db, tools.NewWorkspaceTools(slog.Default()), allowAllPolicies(), nil, nil, slog.Default())
	classification := runfailure.Classification{
		Taxonomy:    runfailure.TaxonomyVersion,
		Category:    runfailure.CategoryInfrastructure,
		Retryable:   true,
		Disposition: runfailure.DispositionRetry,
		Stage:       "verification",
		Source:      "tests",
	}

	event, err := runner.buildFailedRunEvent(context.Background(), "run-1", "connection reset by peer", classification)
	if err != nil {
		t.Fatalf("buildFailedRunEvent() error: %v", err)
	}
	if event.RunID != "run-1" || event.TaskID != "task-1" || event.AgentRole != models.AgentRoleImplementer || event.Status != models.AgentRunStatusFailed {
		t.Fatalf("event envelope = %+v", event)
	}
	var data struct {
		Error   string                    `json:"error"`
		Failure runfailure.Classification `json:"failure"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("decode event data: %v", err)
	}
	if data.Error != "connection reset by peer" || data.Failure.Category != runfailure.CategoryInfrastructure || !data.Failure.Retryable {
		t.Fatalf("event data = %+v", data)
	}
}
