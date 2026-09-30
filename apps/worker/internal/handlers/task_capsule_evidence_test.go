package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/scheduler"
)

type memoryCapsuleEvidenceStore struct {
	record dbpkg.TaskCapsuleRecord
	leases []dbpkg.TaskLeaseRecord
	loadErr error
	upsertErr error
	upserts int
}

func (s *memoryCapsuleEvidenceStore) LoadTaskCapsule(_ context.Context, _ string) (dbpkg.TaskCapsuleRecord, []dbpkg.TaskLeaseRecord, error) {
	if s.loadErr != nil {
		return dbpkg.TaskCapsuleRecord{}, nil, s.loadErr
	}
	return s.record, append([]dbpkg.TaskLeaseRecord(nil), s.leases...), nil
}

func (s *memoryCapsuleEvidenceStore) UpsertTaskCapsule(_ context.Context, record dbpkg.TaskCapsuleRecord, leases []dbpkg.TaskLeaseRecord) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upserts++
	s.record = record
	s.leases = append([]dbpkg.TaskLeaseRecord(nil), leases...)
	return nil
}

func capsuleRecordForTest(t *testing.T, runID string, capsule scheduler.TaskCapsule) dbpkg.TaskCapsuleRecord {
	t.Helper()
	payload, err := json.Marshal(capsule)
	if err != nil {
		t.Fatalf("marshal capsule: %v", err)
	}
	return dbpkg.TaskCapsuleRecord{
		AgentRunID: runID,
		TaskID: capsule.TaskID,
		WorkspaceID: capsule.WorkspaceID,
		Version: capsule.Version,
		AgentID: capsule.Agent.ID,
		AgentRole: capsule.Agent.Role,
		Payload: payload,
	}
}

func TestRecordTaskCapsuleEvidenceAppendsAndPersistsEvidence(t *testing.T) {
	capsule := scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Agent: scheduler.AgentIdentity{ID: "agent-1", Role: "implementer"},
		Leases: []scheduler.Lease{{Path: "apps/api", Mode: scheduler.LeaseModeExclusive}},
		RequiredEvidence: []string{"tests", "lint"},
	}
	store := &memoryCapsuleEvidenceStore{
		record: capsuleRecordForTest(t, "run-1", capsule),
		leases: []dbpkg.TaskLeaseRecord{{Path: "apps/api", Mode: "exclusive"}},
	}

	got, err := recordTaskCapsuleEvidence(context.Background(), store, "run-1", scheduler.Evidence{
		Name: "tests",
		Kind: "command",
		Status: scheduler.EvidenceStatusPassed,
		Command: "go test ./...",
	})
	if err != nil {
		t.Fatalf("recordTaskCapsuleEvidence() error: %v", err)
	}
	if store.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", store.upserts)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].Name != "tests" {
		t.Fatalf("unexpected evidence: %#v", got.Evidence)
	}

	var persisted scheduler.TaskCapsule
	if err := json.Unmarshal(store.record.Payload, &persisted); err != nil {
		t.Fatalf("unmarshal persisted payload: %v", err)
	}
	if !reflect.DeepEqual(persisted, got) {
		t.Fatalf("persisted capsule = %#v, want %#v", persisted, got)
	}
}

func TestVerifyTaskCapsuleCompletionBlocksMissingAndLatestFailedEvidence(t *testing.T) {
	capsule := scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Agent: scheduler.AgentIdentity{ID: "agent-1", Role: "implementer"},
		RequiredEvidence: []string{"tests", "lint"},
		Evidence: []scheduler.Evidence{
			{Name: "tests", Kind: "command", Status: scheduler.EvidenceStatusPassed},
		},
	}
	store := &memoryCapsuleEvidenceStore{
		record: capsuleRecordForTest(t, "run-1", capsule),
	}

	err := verifyTaskCapsuleCompletion(context.Background(), store, "run-1")
	if err == nil || !strings.Contains(err.Error(), "lint") {
		t.Fatalf("expected missing lint evidence, got %v", err)
	}

	capsule.Evidence = append(capsule.Evidence,
		scheduler.Evidence{Name: "lint", Kind: "command", Status: scheduler.EvidenceStatusPassed},
		scheduler.Evidence{Name: "tests", Kind: "command", Status: scheduler.EvidenceStatusFailed},
	)
	store.record = capsuleRecordForTest(t, "run-1", capsule)

	err = verifyTaskCapsuleCompletion(context.Background(), store, "run-1")
	if err == nil || !strings.Contains(err.Error(), "tests failed") {
		t.Fatalf("expected latest failed tests evidence, got %v", err)
	}
}

func TestVerifyTaskCapsuleCompletionPassesWhenAllLatestEvidencePasses(t *testing.T) {
	capsule := scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Agent: scheduler.AgentIdentity{ID: "agent-1", Role: "implementer"},
		RequiredEvidence: []string{"tests", "lint"},
		Evidence: []scheduler.Evidence{
			{Name: "tests", Kind: "command", Status: scheduler.EvidenceStatusFailed},
			{Name: "tests", Kind: "command", Status: scheduler.EvidenceStatusPassed},
			{Name: "lint", Kind: "command", Status: scheduler.EvidenceStatusPassed},
		},
	}
	store := &memoryCapsuleEvidenceStore{
		record: capsuleRecordForTest(t, "run-1", capsule),
	}

	if err := verifyTaskCapsuleCompletion(context.Background(), store, "run-1"); err != nil {
		t.Fatalf("verifyTaskCapsuleCompletion() error: %v", err)
	}
}

func TestRecordTaskCapsuleEvidenceRejectsPersistenceIdentityDrift(t *testing.T) {
	capsule := scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Agent: scheduler.AgentIdentity{ID: "agent-1", Role: "implementer"},
	}
	record := capsuleRecordForTest(t, "run-1", capsule)
	record.TaskID = "different-task"
	store := &memoryCapsuleEvidenceStore{record: record}

	_, err := recordTaskCapsuleEvidence(context.Background(), store, "run-1", scheduler.Evidence{
		Name: "tests",
		Kind: "command",
		Status: scheduler.EvidenceStatusPassed,
	})
	if err == nil || !strings.Contains(err.Error(), "task identity") {
		t.Fatalf("expected task identity drift error, got %v", err)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", store.upserts)
	}
}

func TestRecordTaskCapsuleEvidencePropagatesStoreFailure(t *testing.T) {
	capsule := scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Agent: scheduler.AgentIdentity{ID: "agent-1", Role: "implementer"},
	}
	store := &memoryCapsuleEvidenceStore{
		record: capsuleRecordForTest(t, "run-1", capsule),
		upsertErr: errors.New("write failed"),
	}

	_, err := recordTaskCapsuleEvidence(context.Background(), store, "run-1", scheduler.Evidence{
		Name: "tests",
		Kind: "command",
		Status: scheduler.EvidenceStatusPassed,
	})
	if err == nil || !errors.Is(err, store.upsertErr) {
		t.Fatalf("expected wrapped store failure, got %v", err)
	}
}
