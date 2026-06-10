package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
)

func TestCreateVoiceTask(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	body, _ := json.Marshal(CreateVoiceTaskRequest{
		RepositoryID: "repo-1",
		Transcript:   "Investigate failed deploy and prepare a rollback plan.",
		Provider:     "whisper",
	})

	mock.ExpectExec("INSERT INTO tasks").
		WithArgs(sqlmock.AnyArg(), "proj-1", "repo-1", "user-1", integrationTypeVoice, nil, "Investigate failed deploy and prepare a rollback plan", "Investigate failed deploy and prepare a rollback plan.", "medium", "low", "main", nil, nil, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := httptest.NewRequest(http.MethodPost, "/projects/proj-1/voice-tasks", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "user-1"}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "proj-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CreateVoiceTask(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}

	var task Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if task.Source != integrationTypeVoice {
		t.Fatalf("expected source %q, got %q", integrationTypeVoice, task.Source)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled expectations: %v", err)
	}
}
