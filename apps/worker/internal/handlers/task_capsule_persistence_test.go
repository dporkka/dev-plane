package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/scheduler"
)

type recordingCapsuleStore struct {
	record dbpkg.TaskCapsuleRecord
	leases []dbpkg.TaskLeaseRecord
	err error
	calls int
}

func (s *recordingCapsuleStore) UpsertTaskCapsule(_ context.Context, record dbpkg.TaskCapsuleRecord, leases []dbpkg.TaskLeaseRecord) error {
	s.calls++
	s.record = record
	s.leases = append([]dbpkg.TaskLeaseRecord(nil), leases...)
	return s.err
}

func TestPersistTaskCapsuleMapsSchedulerContractToDurableStore(t *testing.T) {
	store := &recordingCapsuleStore{}
	capsule := scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Agent: scheduler.AgentIdentity{
			ID: "agent-1",
			Role: "implementer",
			Provider: "openai",
			Model: "gpt-5.6",
		},
		Objective: "Implement the change",
		Leases: []scheduler.Lease{
			{Path: "apps/api", Mode: scheduler.LeaseModeExclusive},
			{Path: "docs", Mode: scheduler.LeaseModeExclusive},
		},
		RequiredEvidence: []string{"tests", "lint"},
	}

	if err := persistTaskCapsule(context.Background(), store, "run-1", capsule); err != nil {
		t.Fatalf("persistTaskCapsule() error: %v", err)
	}

	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
	if store.record.AgentRunID != "run-1" ||
		store.record.TaskID != "task-1" ||
		store.record.WorkspaceID != "workspace-1" ||
		store.record.Version != scheduler.TaskCapsuleVersion ||
		store.record.AgentID != "agent-1" ||
		store.record.AgentRole != "implementer" {
		t.Fatalf("unexpected record: %+v", store.record)
	}
	if !json.Valid(store.record.Payload) {
		t.Fatalf("payload is not valid JSON: %s", store.record.Payload)
	}

	var decoded scheduler.TaskCapsule
	if err := json.Unmarshal(store.record.Payload, &decoded); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !reflect.DeepEqual(decoded, capsule) {
		t.Fatalf("decoded capsule = %#v, want %#v", decoded, capsule)
	}

	wantLeases := []dbpkg.TaskLeaseRecord{
		{Path: "apps/api", Mode: "exclusive"},
		{Path: "docs", Mode: "exclusive"},
	}
	if !reflect.DeepEqual(store.leases, wantLeases) {
		t.Fatalf("leases = %#v, want %#v", store.leases, wantLeases)
	}
}

func TestPersistTaskCapsuleRejectsMissingRunIDBeforeStoreCall(t *testing.T) {
	store := &recordingCapsuleStore{}
	capsule := scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Agent: scheduler.AgentIdentity{ID: "agent-1", Role: "implementer"},
	}

	err := persistTaskCapsule(context.Background(), store, "", capsule)
	if err == nil {
		t.Fatal("expected missing run id error")
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestPersistTaskCapsulePropagatesStoreFailure(t *testing.T) {
	store := &recordingCapsuleStore{err: errors.New("write failed")}
	capsule := scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Agent: scheduler.AgentIdentity{ID: "agent-1", Role: "implementer"},
	}

	err := persistTaskCapsule(context.Background(), store, "run-1", capsule)
	if err == nil || !errors.Is(err, store.err) {
		t.Fatalf("expected wrapped store error, got %v", err)
	}
}
