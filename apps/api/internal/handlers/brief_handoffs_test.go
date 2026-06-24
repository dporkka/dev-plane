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
)

func TestCreateBriefHandoff(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projectID := "proj-1"
	userID := "user-1"
	briefProjectID := "brief-123"

	expectAuthorizeProject(mock, projectID)
	mock.ExpectExec("INSERT INTO tasks").
		WithArgs(sqlmock.AnyArg(), projectID, "repo-1", userID, "dev_plan_brief", &briefProjectID, "Implement Brief", sqlmock.AnyArg(), "high", "medium", "main", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	body, _ := json.Marshal(CreateBriefHandoffRequest{
		RepositoryID:       "repo-1",
		BriefProjectID:     briefProjectID,
		BriefURL:           "https://build.davidporkka.com/public/briefs/example",
		Title:              "Implement Brief",
		Description:        "Use the generated Builder's Brief as implementation context.",
		Priority:           "high",
		RiskLevel:          "medium",
		AcceptanceCriteria: []string{"All brief MVP items are implemented"},
		Documents: []BriefDocument{
			{Slug: "PRD", Title: "Product Requirements"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/projects/"+projectID+"/brief-handoffs", bytes.NewReader(body))
	req = req.WithContext(withTestUser(req.Context()))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CreateBriefHandoff(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}

	var task Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if task.Source != "dev_plan_brief" {
		t.Errorf("expected source dev_plan_brief, got %q", task.Source)
	}
	if task.SourceID == nil || *task.SourceID != briefProjectID {
		t.Errorf("expected source_id %q, got %v", briefProjectID, task.SourceID)
	}
	if task.Title != "Implement Brief" {
		t.Errorf("expected title Implement Brief, got %q", task.Title)
	}
	if len(task.Spec) == 0 {
		t.Error("expected spec to be populated")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCreateBriefHandoff_RequiresBriefPointer(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizeProject(mock, "proj-1")

	body, _ := json.Marshal(CreateBriefHandoffRequest{RepositoryID: "repo-1"})
	req := httptest.NewRequest(http.MethodPost, "/projects/proj-1/brief-handoffs", bytes.NewReader(body))
	req = req.WithContext(withTestUser(req.Context()))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", "proj-1")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.CreateBriefHandoff(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
}
