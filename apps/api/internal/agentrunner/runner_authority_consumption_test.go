package agentrunner

import (
	"context"
	"errors"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/execution"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

func TestConsumeApprovedAuthorityConsumesExactGrantOnce(t *testing.T) {
	db := openAuthorityConsumptionDB(t)
	runner := &Runner{db: db}

	grant := execution.Grant{
		Operation: capability.OpWriteFile,
		Resource:  "src/main.go",
		Revision:  "git-tree:abc123",
	}
	insertApprovedCapabilityGrant(t, db, "approval-1", "run-1", grant)

	consumed, err := runner.consumeApprovedAuthority(context.Background(), "run-1", grant)
	if err != nil {
		t.Fatalf("consumeApprovedAuthority() error = %v", err)
	}
	if !consumed {
		t.Fatal("consumeApprovedAuthority() = false, want true")
	}

	consumed, err = runner.consumeApprovedAuthority(context.Background(), "run-1", grant)
	if err != nil {
		t.Fatalf("consumeApprovedAuthority(second) error = %v", err)
	}
	if consumed {
		t.Fatal("approved authority grant must be single-use")
	}
}

func TestConsumeApprovedAuthorityRejectsResourceOrRevisionDriftWithoutBurningGrant(t *testing.T) {
	db := openAuthorityConsumptionDB(t)
	runner := &Runner{db: db}

	approved := execution.Grant{
		Operation: capability.OpWriteFile,
		Resource:  "src/main.go",
		Revision:  "git-tree:abc123",
	}
	insertApprovedCapabilityGrant(t, db, "approval-1", "run-1", approved)

	for _, requested := range []execution.Grant{
		{Operation: capability.OpWriteFile, Resource: "src/other.go", Revision: "git-tree:abc123"},
		{Operation: capability.OpWriteFile, Resource: "src/main.go", Revision: "git-tree:def456"},
	} {
		consumed, err := runner.consumeApprovedAuthority(context.Background(), "run-1", requested)
		if err != nil {
			t.Fatalf("consumeApprovedAuthority(%#v) error = %v", requested, err)
		}
		if consumed {
			t.Fatalf("drifted grant %#v consumed approval", requested)
		}
	}

	consumed, err := runner.consumeApprovedAuthority(context.Background(), "run-1", approved)
	if err != nil {
		t.Fatalf("consumeApprovedAuthority(exact) error = %v", err)
	}
	if !consumed {
		t.Fatal("drift attempts must not burn the exact approved grant")
	}
}

func TestConsumeApprovedAuthorityRejectsTamperedDigest(t *testing.T) {
	db := openAuthorityConsumptionDB(t)
	runner := &Runner{db: db}

	grant := execution.Grant{
		Operation: capability.OpRunCommand,
		Resource:  "go test ./...",
	}
	raw, err := capabilityApprovalMetadata(&capabilityDecisionError{
		toolName:  "run_command",
		operation: grant.Operation,
		resource:  grant.Resource,
		result: &capability.Result{
			Effect:         policies.EffectAsk,
			RiskLevel:      capability.RiskLevelMedium,
			Reason:         "requires approval",
			RequestedGrant: &grant,
		},
	})
	if err != nil {
		t.Fatalf("capabilityApprovalMetadata() error = %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	metadata["authority_digest"] = "tampered"
	raw, err = json.Marshal(metadata)
	if err != nil {
		t.Fatalf("encode tampered metadata: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO approvals (
			id, agent_run_id, approval_type, response, responded_at, metadata, created_at, updated_at
		) VALUES (?, ?, ?, 'approved', ?, ?, ?, ?)
	`, "approval-1", "run-1", "capability:"+grant.Operation, time.Now().UTC(), string(raw), time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatalf("insert approval: %v", err)
	}

	consumed, err := runner.consumeApprovedAuthority(context.Background(), "run-1", grant)
	if err == nil {
		t.Fatal("consumeApprovedAuthority() error = nil, want tampered digest rejection")
	}
	if consumed {
		t.Fatal("tampered authority metadata must never authorize execution")
	}
}

func TestAuthorizeToolUsesExactApprovedGrantInsteadOfRequestingApprovalAgain(t *testing.T) {
	db := openAuthorityConsumptionDB(t)
	runner := &Runner{
		db:     db,
		kernel: capability.NewKernel(nil, nil, nil, nil),
	}

	run := &models.AgentRun{ID: "run-1", AgentRole: models.AgentRoleImplementer}
	task := &models.Task{ID: "task-1"}
	workspace := &models.Workspace{ID: "workspace-1", RuntimeProvider: "local"}
	input := json.RawMessage(`{"path":"src/main.go","content":"package main"}`)

	grant := execution.Grant{
		Operation: capability.OpWriteFile,
		Resource:  "src/main.go",
	}
	insertApprovedCapabilityGrant(t, db, "approval-1", run.ID, grant)

	if err := runner.authorizeTool(context.Background(), run, task, workspace, "write_file", input); err != nil {
		t.Fatalf("authorizeTool() with exact approval error = %v", err)
	}

	err := runner.authorizeTool(context.Background(), run, task, workspace, "write_file", input)
	var decision *capabilityDecisionError
	if !errors.As(err, &decision) || !decision.requiresApproval() {
		t.Fatalf("authorizeTool() after consumption error = %v, want approval-required decision", err)
	}
}

func openAuthorityConsumptionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec(`
		CREATE TABLE approvals (
			id TEXT PRIMARY KEY,
			agent_run_id TEXT,
			approval_type TEXT NOT NULL,
			response TEXT,
			responded_at TIMESTAMP,
			expires_at TIMESTAMP,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at TIMESTAMP NOT NULL,
			updated_at TIMESTAMP NOT NULL
		)
	`)
	if err != nil {
		t.Fatalf("create approvals table: %v", err)
	}
	return db
}

func insertApprovedCapabilityGrant(t *testing.T, db *sql.DB, approvalID, runID string, grant execution.Grant) {
	t.Helper()
	raw, err := capabilityApprovalMetadata(&capabilityDecisionError{
		toolName:  grant.Operation,
		operation: grant.Operation,
		resource:  grant.Resource,
		result: &capability.Result{
			Effect:         policies.EffectAsk,
			RiskLevel:      capability.RiskLevelMedium,
			Reason:         "requires approval",
			RequestedGrant: &grant,
		},
	})
	if err != nil {
		t.Fatalf("capabilityApprovalMetadata() error = %v", err)
	}

	now := time.Now().UTC()
	_, err = db.Exec(`
		INSERT INTO approvals (
			id, agent_run_id, approval_type, response, responded_at, metadata, created_at, updated_at
		) VALUES (?, ?, ?, 'approved', ?, ?, ?, ?)
	`, approvalID, runID, "capability:"+grant.Operation, now, string(raw), now, now)
	if err != nil {
		t.Fatalf("insert approval: %v", err)
	}
}
