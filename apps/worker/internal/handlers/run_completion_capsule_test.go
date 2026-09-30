package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/scheduler"
)

func TestHandleRunCompletedPersistsRevisionBoundEvidenceBeforeReview(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, "reviewer")

	capsule := scheduler.TaskCapsule{
		Version:          scheduler.TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            scheduler.AgentIdentity{ID: "run-1", Role: "reviewer"},
		RequiredEvidence: []string{"tests", "lint"},
	}
	if err := persistTaskCapsule(context.Background(), dbpkg.NewTaskCapsuleSQLStore(db), "run-1", capsule); err != nil {
		t.Fatal(err)
	}

	reviewService := &fakeReviewer{report: &reviewer.ReviewReport{
		RunID:      "run-1",
		RiskLevel:  "low",
		Approvable: true,
	}}
	handler := NewRunHandler(db, slog.Default(), nil).WithReviewer(reviewService)

	event := map[string]any{
		"run_id":  "run-1",
		"task_id": "task-1",
		"data": map[string]any{
			"subject_revision": "tree:abc123",
			"evidence": []map[string]any{
				{"name": "tests", "kind": "command", "status": "passed", "command": "go test ./...", "subject_revision": "tree:abc123"},
				{"name": "lint", "kind": "command", "status": "passed", "command": "go vet ./...", "subject_revision": "tree:abc123"},
			},
		},
	}
	payload, _ := json.Marshal(event)
	if err := handler.HandleRunCompleted(&nats.Msg{Data: payload}); err != nil {
		t.Fatalf("HandleRunCompleted() error: %v", err)
	}
	if reviewService.runID != "run-1" {
		t.Fatalf("review runID = %q, want run-1", reviewService.runID)
	}

	record, _, err := dbpkg.NewTaskCapsuleSQLStore(db).LoadTaskCapsule(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	var persisted scheduler.TaskCapsule
	if err := json.Unmarshal(record.Payload, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.SubjectRevision != "tree:abc123" {
		t.Fatalf("subject revision = %q", persisted.SubjectRevision)
	}
	if len(persisted.Evidence) != 2 {
		t.Fatalf("evidence = %#v", persisted.Evidence)
	}
}

func TestHandleRunCompletedBlocksReviewWhenRequiredEvidenceFails(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, "reviewer")

	capsule := scheduler.TaskCapsule{
		Version:          scheduler.TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            scheduler.AgentIdentity{ID: "run-1", Role: "reviewer"},
		RequiredEvidence: []string{"tests"},
	}
	if err := persistTaskCapsule(context.Background(), dbpkg.NewTaskCapsuleSQLStore(db), "run-1", capsule); err != nil {
		t.Fatal(err)
	}

	reviewService := &fakeReviewer{}
	handler := NewRunHandler(db, slog.Default(), nil).WithReviewer(reviewService)
	payload := []byte(`{"run_id":"run-1","task_id":"task-1","data":{"subject_revision":"tree:abc123","evidence":[{"name":"tests","kind":"command","status":"failed","command":"go test ./...","subject_revision":"tree:abc123"}]}}`)
	err := handler.HandleRunCompleted(&nats.Msg{Data: payload})
	if err == nil || !strings.Contains(err.Error(), "required evidence tests failed") {
		t.Fatalf("expected completion verification failure, got %v", err)
	}
	if reviewService.runID != "" {
		t.Fatalf("review unexpectedly ran for %q", reviewService.runID)
	}
}

func TestHandleRunCompletedAllowsCapsuleWithoutMachineEvidenceRequirements(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, "reviewer")

	capsule := scheduler.TaskCapsule{
		Version:     scheduler.TaskCapsuleVersion,
		TaskID:      "task-1",
		WorkspaceID: "workspace-1",
		Agent:       scheduler.AgentIdentity{ID: "run-1", Role: "reviewer"},
	}
	if err := persistTaskCapsule(context.Background(), dbpkg.NewTaskCapsuleSQLStore(db), "run-1", capsule); err != nil {
		t.Fatal(err)
	}

	reviewService := &fakeReviewer{report: &reviewer.ReviewReport{
		RunID:      "run-1",
		RiskLevel:  "low",
		Approvable: true,
	}}
	handler := NewRunHandler(db, slog.Default(), nil).WithReviewer(reviewService)

	payload := []byte(`{"run_id":"run-1","task_id":"task-1","data":{"evidence":[],"passed":true}}`)
	if err := handler.HandleRunCompleted(&nats.Msg{Data: payload}); err != nil {
		t.Fatalf("HandleRunCompleted() error: %v", err)
	}
	if reviewService.runID != "run-1" {
		t.Fatalf("review runID = %q, want run-1", reviewService.runID)
	}
}
