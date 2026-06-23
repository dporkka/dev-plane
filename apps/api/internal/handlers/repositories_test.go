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

func TestConnectRepository(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	projectID := "proj-1"
	body, _ := json.Marshal(ConnectRepositoryRequest{Owner: "dporkka", Name: "dev-plane"})
	mock.ExpectExec("INSERT INTO repositories").
		WithArgs(sqlmock.AnyArg(), projectID, "dporkka", "dev-plane", "dporkka/dev-plane", "https://github.com/dporkka/dev-plane.git", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req := repositoryRequest(http.MethodPost, "/projects/"+projectID+"/repositories", projectID, bytes.NewReader(body)).
		WithContext(auth.WithUser(context.Background(), &auth.Claims{UserID: "user-1"}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ConnectRepository(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d: %s", http.StatusCreated, rec.Code, rec.Body.String())
	}
	var repo Repository
	if err := json.Unmarshal(rec.Body.Bytes(), &repo); err != nil {
		t.Fatalf("decode repository: %v", err)
	}
	if repo.Owner != "dporkka" || repo.Name != "dev-plane" || repo.CloneURL != "https://github.com/dporkka/dev-plane.git" {
		t.Fatalf("unexpected repository response: %+v", repo)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unfulfilled expectations: %v", err)
	}
}

func TestConnectRepositoryRejectsInvalidOwnerAndName(t *testing.T) {
	cases := []struct {
		name string
		req  ConnectRepositoryRequest
	}{
		{name: "owner path", req: ConnectRepositoryRequest{Owner: "acme/evil", Name: "repo"}},
		{name: "owner leading hyphen", req: ConnectRepositoryRequest{Owner: "-acme", Name: "repo"}},
		{name: "repo path", req: ConnectRepositoryRequest{Owner: "acme", Name: "../repo"}},
		{name: "repo slash", req: ConnectRepositoryRequest{Owner: "acme", Name: "team/repo"}},
		{name: "repo shell", req: ConnectRepositoryRequest{Owner: "acme", Name: "repo;rm"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, mock, cleanup := setupTest(t)
			defer cleanup()

			projectID := "proj-1"
			body, _ := json.Marshal(tc.req)
			req := repositoryRequest(http.MethodPost, "/projects/"+projectID+"/repositories", projectID, bytes.NewReader(body)).
				WithContext(auth.WithUser(context.Background(), &auth.Claims{UserID: "user-1"}))
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("projectID", projectID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()

			h.ConnectRepository(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unexpected database interaction: %v", err)
			}
		})
	}
}

func repositoryRequest(method, target, projectID string, body *bytes.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("projectID", projectID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
