package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/scheduler"
)

func TestTaskCapsuleCompletionObserverRecordsRevisionBoundEvidence(t *testing.T) {
	capsule := scheduler.TaskCapsule{
		Version:          scheduler.TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            scheduler.AgentIdentity{ID: "run-1", Role: "implementer"},
		RequiredEvidence: []string{"tests", "typecheck"},
	}
	store := &memoryCapsuleEvidenceStore{
		record: capsuleRecordForTest(t, "run-1", capsule),
	}
	observer := NewTaskCapsuleCompletionObserver(store)

	err := observer.RecordRunCompletion(context.Background(), "run-1", "git-tree:abc123", []scheduler.Evidence{
		{Name: "tests", Kind: "command", Status: scheduler.EvidenceStatusPassed, Command: "go test ./..."},
		{Name: "typecheck", Kind: "command", Status: scheduler.EvidenceStatusPassed, Command: "go vet ./..."},
	})
	if err != nil {
		t.Fatalf("RecordRunCompletion() error: %v", err)
	}

	got, err := loadTaskCapsuleForRun(context.Background(), store, "run-1")
	if err != nil {
		t.Fatalf("loadTaskCapsuleForRun() error: %v", err)
	}
	if got.SubjectRevision != "git-tree:abc123" {
		t.Fatalf("subject revision = %q, want git-tree:abc123", got.SubjectRevision)
	}
	if len(got.Evidence) != 2 {
		t.Fatalf("evidence count = %d, want 2", len(got.Evidence))
	}
	for _, evidence := range got.Evidence {
		if evidence.SubjectRevision != "git-tree:abc123" {
			t.Fatalf("evidence %q revision = %q", evidence.Name, evidence.SubjectRevision)
		}
	}
}

func TestTaskCapsuleCompletionObserverFailsClosedOnMissingEvidence(t *testing.T) {
	capsule := scheduler.TaskCapsule{
		Version:          scheduler.TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            scheduler.AgentIdentity{ID: "run-1", Role: "implementer"},
		RequiredEvidence: []string{"tests", "lint"},
	}
	store := &memoryCapsuleEvidenceStore{
		record: capsuleRecordForTest(t, "run-1", capsule),
	}
	observer := NewTaskCapsuleCompletionObserver(store)

	err := observer.RecordRunCompletion(context.Background(), "run-1", "git-tree:abc123", []scheduler.Evidence{
		{Name: "tests", Kind: "command", Status: scheduler.EvidenceStatusPassed},
	})
	if err == nil || !strings.Contains(err.Error(), "required evidence lint is missing") {
		t.Fatalf("error = %v, want missing lint evidence", err)
	}
}

func TestTaskCapsuleCompletionObserverFailsClosedOnFailedEvidence(t *testing.T) {
	capsule := scheduler.TaskCapsule{
		Version:          scheduler.TaskCapsuleVersion,
		TaskID:           "task-1",
		WorkspaceID:      "workspace-1",
		Agent:            scheduler.AgentIdentity{ID: "run-1", Role: "implementer"},
		RequiredEvidence: []string{"tests"},
	}
	store := &memoryCapsuleEvidenceStore{
		record: capsuleRecordForTest(t, "run-1", capsule),
	}
	observer := NewTaskCapsuleCompletionObserver(store)

	err := observer.RecordRunCompletion(context.Background(), "run-1", "git-tree:abc123", []scheduler.Evidence{
		{Name: "tests", Kind: "command", Status: scheduler.EvidenceStatusFailed},
	})
	if err == nil || !strings.Contains(err.Error(), "required evidence tests failed") {
		t.Fatalf("error = %v, want failed tests evidence", err)
	}
}
