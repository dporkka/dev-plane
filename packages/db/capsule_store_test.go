package db

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTaskCapsuleStoreUpsertIsIdempotentAndReplacesLeases(t *testing.T) {
	database, err := New("file:" + filepath.Join(t.TempDir(), "capsules.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error: %v", err)
	}
	if _, err := database.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}

	ctx := context.Background()
	first := TaskCapsuleRecord{
		AgentRunID: "run-1",
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Version: 1,
		AgentID: "agent-1",
		AgentRole: "implementer",
		Payload: json.RawMessage(`{"version":1,"task_id":"task-1","objective":"first"}`),
	}
	if err := database.UpsertTaskCapsule(ctx, first, []TaskLeaseRecord{
		{Path: "apps/api", Mode: "exclusive"},
		{Path: "packages/shared", Mode: "exclusive"},
	}); err != nil {
		t.Fatalf("first UpsertTaskCapsule() error: %v", err)
	}

	second := first
	second.Payload = json.RawMessage(`{"version":1,"task_id":"task-1","objective":"updated"}`)
	if err := database.UpsertTaskCapsule(ctx, second, []TaskLeaseRecord{
		{Path: "apps/api", Mode: "exclusive"},
		{Path: "docs", Mode: "exclusive"},
	}); err != nil {
		t.Fatalf("second UpsertTaskCapsule() error: %v", err)
	}

	got, leases, err := database.LoadTaskCapsule(ctx, "run-1")
	if err != nil {
		t.Fatalf("LoadTaskCapsule() error: %v", err)
	}
	if string(got.Payload) != string(second.Payload) {
		t.Fatalf("payload = %s, want %s", got.Payload, second.Payload)
	}
	wantLeases := []TaskLeaseRecord{
		{Path: "apps/api", Mode: "exclusive"},
		{Path: "docs", Mode: "exclusive"},
	}
	if !reflect.DeepEqual(leases, wantLeases) {
		t.Fatalf("leases = %#v, want %#v", leases, wantLeases)
	}

	var capsuleCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM task_capsules WHERE agent_run_id = ?", "run-1").Scan(&capsuleCount); err != nil {
		t.Fatalf("count capsules: %v", err)
	}
	if capsuleCount != 1 {
		t.Fatalf("capsule count = %d, want 1", capsuleCount)
	}
}

func TestTaskCapsuleStoreRejectsMalformedRecordsBeforeWriting(t *testing.T) {
	database, err := New("file:" + filepath.Join(t.TempDir(), "capsules.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error: %v", err)
	}

	ctx := context.Background()
	err = database.UpsertTaskCapsule(ctx, TaskCapsuleRecord{
		AgentRunID: "run-1",
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Version: 1,
		AgentID: "agent-1",
		AgentRole: "implementer",
		Payload: json.RawMessage(`{"version":1}`),
	}, []TaskLeaseRecord{{Path: "../outside", Mode: "exclusive"}})
	if err == nil {
		t.Fatal("expected invalid lease path to be rejected")
	}

	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM task_capsules").Scan(&count); err != nil {
		t.Fatalf("count capsules: %v", err)
	}
	if count != 0 {
		t.Fatalf("capsule count = %d, want 0 after rejected write", count)
	}
}

func TestTaskCapsuleStoreRequiresValidJSONPayload(t *testing.T) {
	database, err := New("file:" + filepath.Join(t.TempDir(), "capsules.db"))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer database.Close()

	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatalf("RunMigrations() error: %v", err)
	}

	err = database.UpsertTaskCapsule(context.Background(), TaskCapsuleRecord{
		AgentRunID: "run-1",
		TaskID: "task-1",
		WorkspaceID: "workspace-1",
		Version: 1,
		AgentID: "agent-1",
		AgentRole: "implementer",
		Payload: json.RawMessage(`{"broken"`),
	}, nil)
	if err == nil {
		t.Fatal("expected invalid JSON payload to be rejected")
	}
}
