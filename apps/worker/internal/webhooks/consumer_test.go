package webhooks

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/events"
)

type fakeEventPublisher struct {
	subjects []string
	datas    [][]byte
	err      error
}

func (p *fakeEventPublisher) Publish(subject string, data []byte) error {
	p.subjects = append(p.subjects, subject)
	p.datas = append(p.datas, append([]byte(nil), data...))
	return p.err
}

func (p *fakeEventPublisher) hasSubject(subject string) bool {
	for _, s := range p.subjects {
		if s == subject {
			return true
		}
	}
	return false
}

func (p *fakeEventPublisher) dataFor(subject string) []byte {
	for i, s := range p.subjects {
		if s == subject {
			return p.datas[i]
		}
	}
	return nil
}

func setupConsumerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE organizations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			slug TEXT NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE projects (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			name TEXT NOT NULL,
			deleted_at DATETIME
		);
		CREATE TABLE users (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			email TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'member',
			created_at DATETIME,
			deleted_at DATETIME
		);
		CREATE TABLE repositories (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			full_name TEXT NOT NULL,
			clone_url TEXT NOT NULL,
			default_branch TEXT NOT NULL DEFAULT 'main',
			deleted_at DATETIME
		);
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			created_by TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'web',
			source_id TEXT,
			title TEXT NOT NULL,
			description TEXT,
			status TEXT NOT NULL,
			priority TEXT NOT NULL DEFAULT 'medium',
			risk_level TEXT NOT NULL DEFAULT 'low',
			target_branch TEXT NOT NULL DEFAULT 'main',
			spec TEXT,
			acceptance_criteria TEXT,
			approval_requirements TEXT,
			metadata TEXT,
			max_runtime_minutes INTEGER DEFAULT 60,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func insertWebhookFixtures(t *testing.T, db *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	_, err := db.Exec(`
		INSERT INTO organizations (id, name, slug) VALUES ('org-1', 'Acme', 'acme');
		INSERT INTO projects (id, organization_id, name) VALUES ('proj-1', 'org-1', 'App');
		INSERT INTO users (id, organization_id, email, role, created_at) VALUES ('user-1', 'org-1', 'admin@acme.test', 'admin', $1);
		INSERT INTO repositories (id, project_id, full_name, clone_url, default_branch)
		VALUES ('repo-1', 'proj-1', 'acme/app', 'https://github.com/acme/app.git', 'main');
	`, now)
	if err != nil {
		t.Fatalf("insert fixtures: %v", err)
	}
}

func TestHandleGitHubIssueOpenedCreatesTask(t *testing.T) {
	db := setupConsumerDB(t)
	defer db.Close()
	insertWebhookFixtures(t, db)

	publisher := &fakeEventPublisher{}
	consumer := NewConsumer(db, slog.Default(), publisher)

	payload, _ := json.Marshal(map[string]any{
		"action": "opened",
		"issue": map[string]any{
			"number": 42,
			"title":  "Fix login bug",
			"body":   "Users cannot log in with SSO",
			"state":  "open",
		},
	})
	event := events.WebhookEvent{
		Source:       "github",
		EventType:    "issues",
		DeliveryID:   "delivery-1",
		RepositoryID: "acme/app",
		Payload:      payload,
	}
	data, _ := json.Marshal(event)
	msg := &nats.Msg{Data: data, Sub: &nats.Subscription{}} // no Reply -> ack is no-op

	if err := consumer.Handle(msg); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	var taskID, title, source, sourceID, status string
	err := db.QueryRow(`
		SELECT id, title, source, source_id, status FROM tasks
		WHERE source_id = '42'
	`).Scan(&taskID, &title, &source, &sourceID, &status)
	if err != nil {
		t.Fatalf("task not created: %v", err)
	}
	if title != "Fix login bug" {
		t.Fatalf("title = %q, want Fix login bug", title)
	}
	if source != "github_issue" {
		t.Fatalf("source = %q, want github_issue", source)
	}
	if status != "backlog" {
		t.Fatalf("status = %q, want backlog", status)
	}

	if !publisher.hasSubject(events.TaskCreated) {
		t.Fatalf("expected %q to be published, got %v", events.TaskCreated, publisher.subjects)
	}
	var taskEvent events.TaskEvent
	if err := json.Unmarshal(publisher.dataFor(events.TaskCreated), &taskEvent); err != nil {
		t.Fatalf("unmarshal task event: %v", err)
	}
	if taskEvent.TaskID != taskID {
		t.Fatalf("task event task_id = %q, want %q", taskEvent.TaskID, taskID)
	}
}

func TestHandleGitHubIssueClosedIgnored(t *testing.T) {
	db := setupConsumerDB(t)
	defer db.Close()
	insertWebhookFixtures(t, db)

	publisher := &fakeEventPublisher{}
	consumer := NewConsumer(db, slog.Default(), publisher)

	payload, _ := json.Marshal(map[string]any{
		"action": "opened",
		"issue": map[string]any{
			"number": 43,
			"title":  "Closed issue",
			"state":  "closed",
		},
	})
	event := events.WebhookEvent{
		Source:       "github",
		EventType:    "issues",
		DeliveryID:   "delivery-2",
		RepositoryID: "acme/app",
		Payload:      payload,
	}
	data, _ := json.Marshal(event)
	msg := &nats.Msg{Data: data, Sub: &nats.Subscription{}}

	if err := consumer.Handle(msg); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE source_id = '43'`).Scan(&count); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no task created, got %d", count)
	}
}

func TestHandleUnknownSourceAcks(t *testing.T) {
	db := setupConsumerDB(t)
	defer db.Close()

	publisher := &fakeEventPublisher{}
	consumer := NewConsumer(db, slog.Default(), publisher)

	event := events.WebhookEvent{
		Source:     "unknown",
		EventType:  "ping",
		DeliveryID: "delivery-3",
		Payload:    []byte(`{}`),
	}
	data, _ := json.Marshal(event)
	msg := &nats.Msg{Data: data, Sub: &nats.Subscription{}}

	if err := consumer.Handle(msg); err != nil {
		t.Fatalf("Handle() error: %v", err)
	}
}
