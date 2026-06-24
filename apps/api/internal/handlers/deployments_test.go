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
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/gateway"
)

type fakeDeployGateway struct {
	deployment *gateway.Deployment
	err        error
}

func (g *fakeDeployGateway) CreateDeployment(ctx context.Context, token *oauth2.Token, owner, name, environment, ref string) (*gateway.Deployment, error) {
	return g.deployment, g.err
}

func TestDeployTaskSuccess(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	h.WithDeployToken("test-deploy-token").WithDeployGateway(&fakeDeployGateway{
		deployment: &gateway.Deployment{
			ID:  12345,
			URL: "https://github.com/owner/repo/deployments/12345",
		},
	})

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)
	mock.ExpectQuery("SELECT status, repository_id, target_branch, project_id").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "repository_id", "target_branch", "project_id"}).
			AddRow("pr_created", "repo-1", "main", "proj-1"))
	mock.ExpectQuery("SELECT owner, name FROM repositories").
		WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"owner", "name"}).AddRow("owner", "repo"))
	mock.ExpectExec("INSERT INTO deployments").
		WithArgs(sqlmock.AnyArg(), taskID, "staging", "main", "12345", "https://github.com/owner/repo/deployments/12345", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE tasks SET status = 'deploying'").
		WithArgs(sqlmock.AnyArg(), taskID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	reqBody, _ := json.Marshal(DeployTaskRequest{Environment: "staging"})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/deploy", bytes.NewReader(reqBody))
	adminCtx := auth.WithUser(req.Context(), &auth.Claims{
		UserID: testUserID,
		OrgID:  testOrgID,
		Email:  "admin@example.com",
		Role:   "admin",
	})
	req = req.WithContext(adminCtx)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.DeployTask(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestDeployTaskWrongStatus(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)
	mock.ExpectQuery("SELECT status, repository_id, target_branch, project_id").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"status", "repository_id", "target_branch", "project_id"}).
			AddRow("running", "repo-1", "main", "proj-1"))

	reqBody, _ := json.Marshal(DeployTaskRequest{Environment: "staging"})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/deploy", bytes.NewReader(reqBody))
	req = req.WithContext(withTestUser(req.Context()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.DeployTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDeployTaskMissingEnvironment(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	taskID := "task-1"
	expectAuthorizeTask(mock, taskID)

	reqBody, _ := json.Marshal(DeployTaskRequest{})
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", taskID)
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+taskID+"/deploy", bytes.NewReader(reqBody))
	req = req.WithContext(withTestUser(req.Context()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.DeployTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
