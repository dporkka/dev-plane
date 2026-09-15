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

	"github.com/DATA-DOG/go-sqlmock"
)

func TestNotifyExternalTaskCallbackSignsOptedInEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	spec := `{"source":"api-factory","source_id":"idea-1","callback":{"enabled":true,"events":["build.pr_created"]},"api_factory":{"slug":"parcel-api"}}`
	mock.ExpectQuery("SELECT spec FROM tasks").
		WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{"spec"}).AddRow(spec))

	secret := "shared-secret"
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Fatal(readErr)
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
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv(externalCallbackURLEnv, server.URL)
	t.Setenv(externalCallbackSecretEnv, secret)

	err = notifyExternalTaskCallback(context.Background(), db, slog.Default(), "task-1", externalCallbackOptions{
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
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyExternalTaskCallbackSkipsUnrequestedEvent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	spec := `{"callback":{"enabled":true,"events":["build.pr_created"]}}`
	mock.ExpectQuery("SELECT spec FROM tasks").
		WithArgs("task-1").
		WillReturnRows(sqlmock.NewRows([]string{"spec"}).AddRow(spec))

	// Configure the feature so the task-level event filter is actually evaluated.
	// No request is sent because build.failed was not requested by the task.
	t.Setenv(externalCallbackURLEnv, "http://127.0.0.1:1/unused")
	t.Setenv(externalCallbackSecretEnv, "secret")

	if err := notifyExternalTaskCallback(context.Background(), db, slog.Default(), "task-1", externalCallbackOptions{EventType: "build.failed"}); err != nil {
		t.Fatalf("unexpected error for filtered event: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyExternalTaskCallbackUnconfiguredAddsNoDatabaseWork(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	t.Setenv(externalCallbackURLEnv, "")
	t.Setenv(externalCallbackSecretEnv, "")

	if err := notifyExternalTaskCallback(context.Background(), db, slog.Default(), "task-1", externalCallbackOptions{EventType: "build.failed"}); err != nil {
		t.Fatalf("unexpected error for disabled callback integration: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadExternalCallbackSpecMissingTaskIsNoop(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT spec FROM tasks").
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)

	_, enabled, err := loadExternalCallbackSpec(context.Background(), db, "missing", "build.failed")
	if err != nil {
		t.Fatal(err)
	}
	if enabled {
		t.Fatal("expected callback to be disabled for missing task")
	}
}
