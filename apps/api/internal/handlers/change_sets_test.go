package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/decisionpacket"
	"github.com/ai-dev-control-plane/models"
)

func newChangeSetRequest(method, path, idParam, id, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{
		UserID: testUserID, OrgID: testOrgID, Email: "test@example.com", Role: models.RoleOwner,
	}))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(idParam, id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

func changeSetPacket(t *testing.T, candidateID, prID, repoID, commitSHA, treeHash string, approvable bool, createdAt time.Time) (string, string) {
	t.Helper()
	packet, err := decisionpacket.New(decisionpacket.Input{
		Candidate: decisionpacket.Candidate{
			ID: candidateID, PullRequestID: prID, TaskID: "task-" + candidateID, RunID: "run-" + candidateID,
			RepositoryID: repoID, CommitSHA: commitSHA, TreeHash: treeHash, Branch: "feature-" + candidateID,
		},
		Task:   decisionpacket.TaskSnapshot{Title: candidateID},
		Review: decisionpacket.ReviewSnapshot{RiskLevel: "low", Approvable: approvable},
		Verification: decisionpacket.VerificationSnapshot{
			ContractHash: "contract-a", EnvironmentDigest: "env-a", RunnerIdentity: "runtime:runner-1",
			Checks: json.RawMessage(`[{"id":"unit","passed":true,"exit_code":0}]`),
		},
		CreatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("decisionpacket.New() error: %v", err)
	}
	digest, err := packet.Digest()
	if err != nil {
		t.Fatalf("packet.Digest() error: %v", err)
	}
	data, err := packet.Marshal()
	if err != nil {
		t.Fatalf("packet.Marshal() error: %v", err)
	}
	return digest, string(data)
}

func expectDraftChangeSet(mock sqlmock.Sqlmock, id string) {
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT id, project_id, name, description, status").
		WithArgs(id).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "name", "description", "status", "publication_digest",
			"publication_manifest", "authorized_at", "authorized_by", "created_by", "created_at", "updated_at",
		}).AddRow(id, "project-1", "Release 1", nil, "draft", nil, nil, nil, nil, testUserID, now, now))
}

func expectReadyChangeSetMembers(t *testing.T, mock sqlmock.Sqlmock, changeSetID string, now time.Time) {
	t.Helper()
	apiDigest, apiPacket := changeSetPacket(t, "candidate-api", "pr-api", "repo-api", "sha-api", "tree-api", true, now)
	webDigest, webPacket := changeSetPacket(t, "candidate-web", "pr-web", "repo-web", "sha-web", "tree-web", true, now)
	mock.ExpectQuery("SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash").
		WithArgs(changeSetID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pull_request_id", "repository_id", "commit_sha", "tree_hash",
			"decision_digest", "packet", "state",
		}).
			AddRow("candidate-api", "pr-api", "repo-api", "sha-api", "tree-api", apiDigest, apiPacket, "open").
			AddRow("candidate-web", "pr-web", "repo-web", "sha-web", "tree-web", webDigest, webPacket, "open"))
}

func expectChangeSetGraph(mock sqlmock.Sqlmock, schemaState string) {
	mock.ExpectQuery("SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash").
		WithArgs("project-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pull_request_id", "repository_id", "commit_sha", "tree_hash", "state", "depends_on_candidate_id",
		}).
			AddRow("candidate-schema", "pr-schema", "repo-db", "sha-schema", "tree-schema", schemaState, nil).
			AddRow("candidate-api", "pr-api", "repo-api", "sha-api", "tree-api", "open", "candidate-schema").
			AddRow("candidate-web", "pr-web", "repo-web", "sha-web", "tree-web", "open", "candidate-api"))
}

func TestCreateChangeSet(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectAuthorizeProject(mock, "project-1")
	mock.ExpectExec("INSERT INTO change_sets").
		WithArgs(sqlmock.AnyArg(), "project-1", "Release 1", nil, "draft", testUserID, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.CreateChangeSet(rec, newChangeSetRequest(
		http.MethodPost, "/projects/project-1/change-sets", "projectID", "project-1",
		"{\"name\":\"Release 1\"}",
	))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestAddChangeSetCandidate(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()

	expectDraftChangeSet(mock, "set-1")
	expectAuthorizeProject(mock, "project-1")
	expectAuthorizePullRequest(mock, "pr-api")
	mock.ExpectQuery("SELECT c.id, t.project_id, pr.state").
		WithArgs("pr-api").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "state"}).
			AddRow("candidate-api", "project-1", "open"))
	mock.ExpectExec("INSERT INTO change_set_candidates").
		WithArgs("set-1", "candidate-api", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.AddChangeSetCandidate(rec, newChangeSetRequest(
		http.MethodPost, "/change-sets/set-1/candidates", "id", "set-1",
		"{\"pull_request_id\":\"pr-api\"}",
	))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"candidate_id\":\"candidate-api\"") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestAuthorizeChangeSetPublication(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	now := time.Now().UTC()

	expectDraftChangeSet(mock, "set-1")
	expectAuthorizeProject(mock, "project-1")
	expectReadyChangeSetMembers(t, mock, "set-1", now)
	expectChangeSetGraph(mock, "merged")
	mock.ExpectExec("UPDATE change_sets").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), testUserID, "set-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.AuthorizeChangeSetPublication(rec, newChangeSetRequest(
		http.MethodPost, "/change-sets/set-1/authorize-publication", "id", "set-1", "",
	))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"status\":\"authorized\"") ||
		!strings.Contains(rec.Body.String(), "\"publication_order\":[\"candidate-api\",\"candidate-web\"]") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestAuthorizeChangeSetPublicationBlocksExternalFrontier(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	now := time.Now().UTC()

	expectDraftChangeSet(mock, "set-1")
	expectAuthorizeProject(mock, "project-1")
	expectReadyChangeSetMembers(t, mock, "set-1", now)
	expectChangeSetGraph(mock, "open")

	rec := httptest.NewRecorder()
	h.AuthorizeChangeSetPublication(rec, newChangeSetRequest(
		http.MethodPost, "/change-sets/set-1/authorize-publication", "id", "set-1", "",
	))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "candidate-schema") ||
		!strings.Contains(rec.Body.String(), "external-dependency-not-merged") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}
