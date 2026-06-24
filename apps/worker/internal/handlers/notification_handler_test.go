package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"
	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/events"
)

func setupNotificationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE organizations (
			id TEXT PRIMARY KEY
		);
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL
		);
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			status TEXT NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE integrations (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			integration_type TEXT NOT NULL,
			credentials_encrypted TEXT,
			config TEXT,
			status TEXT NOT NULL,
			deleted_at DATETIME
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func insertNotificationFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO organizations (id) VALUES ('org-1');
		INSERT INTO projects (id, organization_id) VALUES ('proj-1', 'org-1');
		INSERT INTO tasks (id, project_id, status) VALUES ('task-1', 'proj-1', 'running');
	`)
	if err != nil {
		t.Fatalf("insert fixtures: %v", err)
	}
}

func TestHandleTaskCompletedNoIntegrations(t *testing.T) {
	db := setupNotificationDB(t)
	defer db.Close()
	insertNotificationFixtures(t, db)

	publisher := &fakeWorkerEventPublisher{}
	handler := NewNotificationHandler(db, slog.Default(), publisher)

	event := events.TaskEvent{TaskID: "task-1", Status: "done", ProjectID: "proj-1"}
	data, _ := json.Marshal(event)
	msg := &nats.Msg{Data: data}

	if err := handler.HandleTaskCompleted(msg); err != nil {
		t.Fatalf("HandleTaskCompleted() error: %v", err)
	}
}

func TestHandleApprovalRequestedNoIntegrations(t *testing.T) {
	db := setupNotificationDB(t)
	defer db.Close()
	insertNotificationFixtures(t, db)

	publisher := &fakeWorkerEventPublisher{}
	handler := NewNotificationHandler(db, slog.Default(), publisher)

	data, _ := json.Marshal(map[string]any{
		"task_id":       "task-1",
		"approval_type": "execution",
	})
	msg := &nats.Msg{Data: data}

	if err := handler.HandleApprovalRequested(msg); err != nil {
		t.Fatalf("HandleApprovalRequested() error: %v", err)
	}
}
