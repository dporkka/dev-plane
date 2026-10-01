package db

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestTaskCapsuleSQLStorePersistsThroughRawSQLConnection(t *testing.T) {
	database, err := New("file:" + filepath.Join(t.TempDir(), "capsule-raw.db"))
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

	store := NewTaskCapsuleSQLStore(database.DB)
	record := TaskCapsuleRecord{
		AgentRunID: "run-raw",
		TaskID: "task-raw",
		WorkspaceID: "workspace-raw",
		Version: 1,
		AgentID: "agent-raw",
		AgentRole: "implementer",
		Payload: json.RawMessage(`{"version":1,"task_id":"task-raw"}`),
	}
	leases := []TaskLeaseRecord{{Path: "./apps/api/**", Mode: "exclusive"}}

	if err := store.UpsertTaskCapsule(context.Background(), record, leases); err != nil {
		t.Fatalf("UpsertTaskCapsule() error: %v", err)
	}
	got, gotLeases, err := store.LoadTaskCapsule(context.Background(), "run-raw")
	if err != nil {
		t.Fatalf("LoadTaskCapsule() error: %v", err)
	}
	if got.AgentRunID != "run-raw" || string(got.Payload) != string(record.Payload) {
		t.Fatalf("unexpected record: %+v", got)
	}
	if len(gotLeases) != 1 || gotLeases[0].Path != "apps/api" || gotLeases[0].Mode != "exclusive" {
		t.Fatalf("unexpected leases: %#v", gotLeases)
	}
}
