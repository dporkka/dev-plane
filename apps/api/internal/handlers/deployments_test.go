package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/gateway"
)

type fakeDeployGateway struct {
	deployment   *gateway.Deployment
	err          error
	resolvedSHA  string
	resolveErr   error
	deployments  []gateway.Deployment
	listErr      error
	createCalls  int
}

func (g *fakeDeployGateway) ResolveCommitSHA(ctx context.Context, token *oauth2.Token, owner, name, ref string) (string, error) {
	if g.resolveErr != nil {
		return "", g.resolveErr
	}
	if g.resolvedSHA != "" {
		return g.resolvedSHA, nil
	}
	return "commit-abc", nil
}

func (g *fakeDeployGateway) ListDeployments(ctx context.Context, token *oauth2.Token, owner, name string, opts gateway.DeploymentListOptions) ([]gateway.Deployment, error) {
	if g.listErr != nil {
		return nil, g.listErr
	}
	return append([]gateway.Deployment(nil), g.deployments...), nil
}

func (g *fakeDeployGateway) CreateDeploymentWithPayload(
	ctx context.Context,
	token *oauth2.Token,
	owner, name, environment, ref string,
	payload map[string]any,
) (*gateway.Deployment, error) {
	g.createCalls++
	return g.deployment, g.err
}

func expectNewDeploymentEffect(
	t *testing.T,
	mock sqlmock.Sqlmock,
	input deploymentEffectInput,
	externalID int64,
) {
	t.Helper()

	effectID, err := deploymentEffectID(input)
	if err != nil {
		t.Fatalf("deploymentEffectID() error = %v", err)
	}
	mock.ExpectQuery("SELECT run_id, activation_id, epoch, ordinal").
		WithArgs(string(effectID)).
		WillReturnError(sql.ErrNoRows)

	intent, err := newDeploymentEffectIntent(input)
	if err != nil {
		t.Fatalf("newDeploymentEffectIntent() error = %v", err)
	}
	mock.ExpectExec("INSERT INTO execution_effects").
		WithArgs(
			string(intent.ID),
			intent.RunID,
			intent.ActivationID,
			int64(intent.Epoch),
			int64(intent.Ordinal),
			intent.Grant.Operation,
			intent.Grant.Resource,
			intent.Grant.Revision,
			intent.InputDigest,
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	expectLoad := func() {
		mock.ExpectQuery("SELECT run_id, activation_id, epoch, ordinal").
			WithArgs(string(intent.ID)).
			WillReturnRows(sqlmock.NewRows([]string{
				"run_id", "activation_id", "epoch", "ordinal",
				"operation", "resource", "revision", "input_digest",
				"provider", "reference", "output_digest", "created_at", "completed_at",
			}).AddRow(
				intent.RunID,
				intent.ActivationID,
				int64(intent.Epoch),
				int64(intent.Ordinal),
				intent.Grant.Operation,
				intent.Grant.Resource,
				intent.Grant.Revision,
				intent.InputDigest,
				nil, nil, nil, time.Now().UTC(), nil,
			))
	}
	expectLoad()
	expectLoad()

	deployment := &gateway.Deployment{
		ID:          externalID,
		SHA:         input.CommitSHA,
		Environment: input.Environment,
	}
	receipt, err := newDeploymentEffectReceipt(intent, deployment)
	if err != nil {
		t.Fatalf("newDeploymentEffectReceipt() error = %v", err)
	}
	expectLoad()
	mock.ExpectExec("UPDATE execution_effects").
		WithArgs(
			receipt.Provider,
			receipt.Reference,
			receipt.OutputDigest,
			sqlmock.AnyArg(),
			string(receipt.EffectID),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestDeployTaskSuccess(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	h.WithDeployToken("test-deploy-token").WithDeployGateway(&fakeDeployGateway{
		deployment: &gateway.Deployment{
			ID:          12345,
			URL:         "https://github.com/owner/repo/deployments/12345",
			SHA:         "commit-abc",
			Ref:         "commit-abc",
			Environment: "staging",
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
	expectNewDeploymentEffect(t, mock, deploymentEffectInput{
		RunID:       taskID,
		TaskID:      taskID,
		ProjectID:   "proj-1",
		RepoID:      "repo-1",
		Owner:       "owner",
		RepoName:    "repo",
		Environment: "staging",
		CommitSHA:   "commit-abc",
	}, 12345)
	mock.ExpectExec("INSERT INTO deployments").
		WithArgs(
			sqlmock.AnyArg(),
			taskID,
			"staging",
			"commit-abc",
			"12345",
			"https://github.com/owner/repo/deployments/12345",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT id, task_id, environment, ref, created_at").
		WithArgs("12345").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "environment", "ref", "created_at",
		}).AddRow("deployment-local-1", taskID, "staging", "commit-abc", time.Now().UTC()))
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
