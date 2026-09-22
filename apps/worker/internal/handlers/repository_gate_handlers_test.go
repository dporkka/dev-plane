package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/repogate"
)

type fakeRepositoryGate struct {
	prepareRecord   repogate.Record
	prepareErr      error
	integrateRecord repogate.Record
	integrateErr    error
	prepareRunID    string
	prepareTaskID   string
	integrateRunID  string
	integrateTaskID string
}

func (g *fakeRepositoryGate) PrepareReviewedCandidate(_ context.Context, runID, taskID string) (repogate.Record, error) {
	g.prepareRunID = runID
	g.prepareTaskID = taskID
	return g.prepareRecord, g.prepareErr
}

func (g *fakeRepositoryGate) IntegrateApproved(_ context.Context, runID, taskID string) (repogate.Record, error) {
	g.integrateRunID = runID
	g.integrateTaskID = taskID
	return g.integrateRecord, g.integrateErr
}

type fakeRunBoundPRCreator struct {
	genericTaskID string
	taskID        string
	runID         string
	pr            *models.PullRequest
	err           error
}

func (f *fakeRunBoundPRCreator) CreatePullRequest(_ context.Context, taskID string) (*models.PullRequest, error) {
	f.genericTaskID = taskID
	return nil, errors.New("generic pull request creation must not be used with repository gate")
}

func (f *fakeRunBoundPRCreator) CreatePullRequestForRun(_ context.Context, taskID, runID string) (*models.PullRequest, error) {
	f.taskID = taskID
	f.runID = runID
	if f.err != nil {
		return nil, f.err
	}
	if f.pr != nil {
		return f.pr, nil
	}
	r := runID
	return &models.PullRequest{ID: "pr-1", TaskID: taskID, RunID: &r, Number: 42}, nil
}

func TestHandleReviewCompletedPreparesCandidateBeforeApproval(t *testing.T) {
	db := setupReviewGateDB(t)
	defer db.Close()

	gate := &fakeRepositoryGate{prepareRecord: repogate.Record{
		RunID: "run-1", TaskID: "task-1", CommitID: "candidate-commit",
		ChangeID: "change-1", Status: repogate.StatusVerified,
	}}
	handler := NewRunHandler(db, slog.Default(), nil).WithRepositoryGate(gate)

	err := handler.HandleReviewCompleted(&nats.Msg{Data: []byte(`{
		"run_id":"run-1",
		"task_id":"task-1",
		"status":"completed",
		"risk_level":"low",
		"approvable":true
	}`)})
	if err != nil {
		t.Fatalf("HandleReviewCompleted() error: %v", err)
	}
	if gate.prepareRunID != "run-1" || gate.prepareTaskID != "task-1" {
		t.Fatalf("prepare called with run/task %q/%q", gate.prepareRunID, gate.prepareTaskID)
	}

	var runID, metadata string
	if err := db.QueryRow(`
		SELECT agent_run_id, metadata FROM approvals WHERE task_id = 'task-1'
	`).Scan(&runID, &metadata); err != nil {
		t.Fatalf("query approval: %v", err)
	}
	if runID != "run-1" {
		t.Fatalf("approval run = %q, want run-1", runID)
	}
	if !contains(metadata, "candidate-commit") || !contains(metadata, "change-1") || !contains(metadata, "verified") {
		t.Fatalf("approval metadata = %s, want candidate provenance", metadata)
	}
}

func TestHandleReviewCompletedDoesNotApproveRejectedCandidate(t *testing.T) {
	db := setupReviewGateDB(t)
	defer db.Close()

	gate := &fakeRepositoryGate{prepareErr: repogate.ErrCandidateRejected}
	handler := NewRunHandler(db, slog.Default(), nil).WithRepositoryGate(gate)

	err := handler.HandleReviewCompleted(&nats.Msg{Data: []byte(`{
		"run_id":"run-1",
		"task_id":"task-1",
		"status":"completed",
		"risk_level":"high",
		"approvable":false
	}`)})
	if err != nil {
		t.Fatalf("HandleReviewCompleted() error: %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM approvals").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("approval count = %d, want 0", count)
	}
}

func TestHandleApprovalApprovedIntegratesExactRunBeforePRCreation(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "reviewing")

	runID := "run-1"
	gate := &fakeRepositoryGate{integrateRecord: repogate.Record{
		RunID: runID, TaskID: "task-1", Status: repogate.StatusIntegrated,
	}}
	creator := &fakeRunBoundPRCreator{pr: &models.PullRequest{
		ID: "pr-1", TaskID: "task-1", RunID: &runID, Number: 42,
	}}
	handler := NewApprovalHandler(db, slog.Default(), nil).
		WithPullRequestCreator(creator).
		WithRepositoryGate(gate)

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"agent_run_id":"run-1",
		"response":"approved",
		"approval_type":"pr_create"
	}`)})
	if err != nil {
		t.Fatalf("HandleApprovalApproved() error: %v", err)
	}
	if gate.integrateRunID != "run-1" || gate.integrateTaskID != "task-1" {
		t.Fatalf("integrate called with run/task %q/%q", gate.integrateRunID, gate.integrateTaskID)
	}
	if creator.taskID != "task-1" || creator.runID != "run-1" {
		t.Fatalf("run-bound creator called with task/run %q/%q", creator.taskID, creator.runID)
	}
	if creator.genericTaskID != "" {
		t.Fatalf("generic creator unexpectedly called with %q", creator.genericTaskID)
	}
}

func TestHandleApprovalApprovedStopsWhenIntegrationFails(t *testing.T) {
	db := setupApprovalHandlerDB(t)
	defer db.Close()
	insertApprovalTaskFixture(t, db, "task-1", "reviewing")

	gate := &fakeRepositoryGate{integrateErr: errors.New("reverification rejected candidate")}
	creator := &fakeRunBoundPRCreator{}
	handler := NewApprovalHandler(db, slog.Default(), nil).
		WithPullRequestCreator(creator).
		WithRepositoryGate(gate)

	err := handler.HandleApprovalApproved(&nats.Msg{Data: []byte(`{
		"approval_id":"approval-1",
		"task_id":"task-1",
		"agent_run_id":"run-1",
		"response":"approved",
		"approval_type":"pr_create"
	}`)})
	if err == nil || !contains(err.Error(), "integrate approved candidate") {
		t.Fatalf("error = %v, want integration failure", err)
	}
	if creator.taskID != "" || creator.genericTaskID != "" {
		t.Fatalf("creator should not be called, task/run = %q/%q", creator.taskID, creator.runID)
	}
}

func setupReviewGateDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE approvals (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			approval_type TEXT NOT NULL,
			requested_by TEXT NOT NULL,
			requested_at DATETIME,
			responded_by TEXT,
			response TEXT,
			response_note TEXT,
			responded_at DATETIME,
			expires_at DATETIME,
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
