package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func openCallbackTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			spec TEXT,
			deleted_at TEXT
		)
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertCallbackTask(t *testing.T, db *sql.DB, id, spec string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tasks (id, spec) VALUES (?, ?)`, id, spec); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyExternalTaskCallbackSignsOptedInEvent(t *testing.T) {
	db := openCallbackTestDB(t)
	spec := `{"source":"api-factory","source_id":"idea-1","callback":{"enabled":true,"events":["build.pr_created"]},"api_factory":{"slug":"parcel-api"}}`
	insertCallbackTask(t, db, "task-1", spec)

	secret := "shared-secret"
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read request body: %v", readErr)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		timestamp := r.Header.Get("X-Dev-Plane-Timestamp")
		provided := r.Header.Get("X-Dev-Plane-Signature")
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(timestamp))
		_, _ = mac.Write([]byte("."))
		_, _ = mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(provided), []byte(expected)) {
			t.Errorf("signature = %q, want %q", provided, expected)
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Errorf("decode callback: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv(externalCallbackURLEnv, server.URL)
	t.Setenv(externalCallbackSecretEnv, secret)

	err := notifyExternalTaskCallback(context.Background(), db, slog.Default(), "task-1", externalCallbackOptions{
		EventID:   "dev-plane:task:task-1:build.pr_created:pr-1",
		EventType: "build.pr_created",
		RunID:     "run-1",
		Status:    "pr_created",
		Artifact: map[string]any{
			"pr_url":    "https://github.com/example/repo/pull/1",
			"pr_number": 1,
			"branch":    "agent/task-1",
		},
	})
	if err != nil {
		t.Fatalf("notifyExternalTaskCallback: %v", err)
	}

	if received["source"] != "api-factory" || received["source_id"] != "idea-1" {
		t.Fatalf("unexpected source payload: %#v", received)
	}
	if received["slug"] != "parcel-api" || received["event_type"] != "build.pr_created" {
		t.Fatalf("unexpected callback payload: %#v", received)
	}
}

func TestNotifyExternalTaskCallbackRetriesTransientFailure(t *testing.T) {
	db := openCallbackTestDB(t)
	spec := `{"source":"api-factory","source_id":"idea-1","callback":{"enabled":true,"events":["build.failed"]}}`
	insertCallbackTask(t, db, "task-1", spec)

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv(externalCallbackURLEnv, server.URL)
	t.Setenv(externalCallbackSecretEnv, "secret")
	t.Setenv("EXTERNAL_TASK_CALLBACK_ATTEMPTS", "2")

	if err := notifyExternalTaskCallback(context.Background(), db, slog.Default(), "task-1", externalCallbackOptions{EventType: "build.failed", Status: "failed"}); err != nil {
		t.Fatalf("notifyExternalTaskCallback: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestNotifyExternalTaskCallbackSkipsUnrequestedEvent(t *testing.T) {
	db := openCallbackTestDB(t)
	spec := `{"callback":{"enabled":true,"events":["build.pr_created"]}}`
	insertCallbackTask(t, db, "task-1", spec)

	// Configure the feature so the task-level event filter is actually evaluated.
	// No request is sent because build.failed was not requested by the task.
	t.Setenv(externalCallbackURLEnv, "http://127.0.0.1:1/unused")
	t.Setenv(externalCallbackSecretEnv, "secret")

	if err := notifyExternalTaskCallback(context.Background(), db, slog.Default(), "task-1", externalCallbackOptions{EventType: "build.failed"}); err != nil {
		t.Fatalf("unexpected error for filtered event: %v", err)
	}
}

func TestNotifyExternalTaskCallbackUnconfiguredAddsNoDatabaseWork(t *testing.T) {
	db := openCallbackTestDB(t)
	t.Setenv(externalCallbackURLEnv, "")
	t.Setenv(externalCallbackSecretEnv, "")

	if err := notifyExternalTaskCallback(context.Background(), db, slog.Default(), "task-1", externalCallbackOptions{EventType: "build.failed"}); err != nil {
		t.Fatalf("unexpected error for disabled callback integration: %v", err)
	}
}

func TestLoadExternalCallbackSpecMissingTaskIsNoop(t *testing.T) {
	db := openCallbackTestDB(t)
	_, enabled, err := loadExternalCallbackSpec(context.Background(), db, "missing", "build.failed")
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("expected callback to be disabled for missing task")
	}
}
