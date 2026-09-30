package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/gateway"
)

type fakePublicationGateway struct {
	getResults []*gateway.GitHubPR
	getErr     error
	getCalls   int
	mergeResult *gateway.MergePRResult
	mergeErr    error
	mergeCalls  []gateway.MergePRRequest
}

func (f *fakePublicationGateway) GetPR(ctx context.Context, token *oauth2.Token, owner, name string, number int) (*gateway.GitHubPR, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if len(f.getResults) == 0 {
		return nil, nil
	}
	idx := f.getCalls
	if idx >= len(f.getResults) {
		idx = len(f.getResults) - 1
	}
	f.getCalls++
	return f.getResults[idx], nil
}

func (f *fakePublicationGateway) MergePR(ctx context.Context, token *oauth2.Token, owner, name string, number int, req gateway.MergePRRequest) (*gateway.MergePRResult, error) {
	f.mergeCalls = append(f.mergeCalls, req)
	if f.mergeErr != nil {
		return nil, f.mergeErr
	}
	return f.mergeResult, nil
}

func publicationRemotePR(state string, merged bool, headSHA, mergeSHA string) *gateway.GitHubPR {
	pr := &gateway.GitHubPR{Number: 42, State: state, Merged: merged, MergeCommitSHA: mergeSHA}
	pr.Head.SHA = headSHA
	pr.Head.Ref = "feature"
	pr.Base.Ref = "main"
	return pr
}

func expectAuthorizedSingleCandidateChangeSet(t *testing.T, mock sqlmock.Sqlmock, publicationStatus string, now time.Time) (decisionDigest, packet, setDigest, setManifest string) {
	t.Helper()
	decisionDigest, packet = decisionPacketFixture(t, "pr-1", "candidate-sha", "tree-a", now)
	setDigest, setManifest = authorizedChangeSetManifestFixture(t, decisionDigest)

	mock.ExpectQuery("SELECT id, project_id, name, description, status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "name", "description", "status", "publication_status",
			"publication_digest", "publication_manifest", "authorized_at", "authorized_by",
			"created_by", "created_at", "updated_at",
		}).AddRow(
			"set-1", "project-1", "Release 1", nil, "authorized", publicationStatus,
			setDigest, setManifest, now, testUserID, testUserID, now, now,
		))
	expectAuthorizeProject(mock, "project-1")

	mock.ExpectQuery("SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pull_request_id", "repository_id", "commit_sha", "tree_hash",
			"decision_digest", "packet", "state",
		}).AddRow("candidate-1", "pr-1", "repo-1", "candidate-sha", "tree-a", decisionDigest, packet, "open"))
	mock.ExpectQuery("SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash").
		WithArgs("project-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pull_request_id", "repository_id", "commit_sha", "tree_hash", "state", "depends_on_candidate_id",
		}).AddRow("candidate-1", "pr-1", "repo-1", "candidate-sha", "tree-a", "open", nil))
	return
}

func TestPublishChangeSet_ReconcilesRemoteMergedMemberAndCompletes(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	now := time.Now().UTC()

	fakeGH := &fakePublicationGateway{
		getResults: []*gateway.GitHubPR{publicationRemotePR("closed", true, "candidate-sha", "merge-sha")},
	}
	h = h.WithGitHubGateway(fakeGH).WithGitHubToken("gh-token")

	expectAuthorizedSingleCandidateChangeSet(t, mock, "pending", now)
	mock.ExpectExec("UPDATE change_sets").
		WithArgs("publishing", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "set-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO change_set_publications").
		WithArgs("set-1", "candidate-1", 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT cp.candidate_id, cp.ordinal, cp.status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"candidate_id", "ordinal", "status", "attempt_count", "merge_sha", "last_error",
			"pull_request_id", "commit_sha", "task_id", "pr_state", "pr_number", "owner", "name",
		}).AddRow(
			"candidate-1", 0, "pending", 0, nil, nil,
			"pr-1", "candidate-sha", "task-1", "open", 42, "owner", "repo",
		))
	mock.ExpectExec("UPDATE change_set_publications").
		WithArgs(sqlmock.AnyArg(), "set-1", "candidate-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE pull_requests SET state = 'merged'").
		WithArgs(sqlmock.AnyArg(), "pr-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE tasks SET status = 'done'").
		WithArgs(sqlmock.AnyArg(), "task-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE change_set_publications SET status = 'merged'").
		WithArgs("merge-sha", sqlmock.AnyArg(), "set-1", "candidate-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE change_sets").
		WithArgs("completed", "completed", sqlmock.AnyArg(), "set-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.PublishChangeSet(rec, newChangeSetRequest(
		http.MethodPost, "/change-sets/set-1/publish", "id", "set-1", "",
	))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"publication_status\":\"completed\"") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if len(fakeGH.mergeCalls) != 0 {
		t.Fatalf("merge calls = %d, want 0 when remote is already merged", len(fakeGH.mergeCalls))
	}
	if fakeGH.getCalls != 1 {
		t.Fatalf("get calls = %d, want 1", fakeGH.getCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestPublishChangeSet_BlocksClosedUnmergedRemoteMember(t *testing.T) {
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	now := time.Now().UTC()

	fakeGH := &fakePublicationGateway{
		getResults: []*gateway.GitHubPR{publicationRemotePR("closed", false, "candidate-sha", "")},
	}
	h = h.WithGitHubGateway(fakeGH).WithGitHubToken("gh-token")

	expectAuthorizedSingleCandidateChangeSet(t, mock, "pending", now)
	mock.ExpectExec("UPDATE change_sets").
		WithArgs("publishing", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "set-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO change_set_publications").
		WithArgs("set-1", "candidate-1", 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT cp.candidate_id, cp.ordinal, cp.status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"candidate_id", "ordinal", "status", "attempt_count", "merge_sha", "last_error",
			"pull_request_id", "commit_sha", "task_id", "pr_state", "pr_number", "owner", "name",
		}).AddRow(
			"candidate-1", 0, "pending", 0, nil, nil,
			"pr-1", "candidate-sha", "task-1", "open", 42, "owner", "repo",
		))
	mock.ExpectExec("UPDATE change_set_publications").
		WithArgs(sqlmock.AnyArg(), "set-1", "candidate-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE change_set_publications SET status = 'blocked'").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "set-1", "candidate-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE change_sets").
		WithArgs("blocked", sqlmock.AnyArg(), "set-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	rec := httptest.NewRecorder()
	h.PublishChangeSet(rec, newChangeSetRequest(
		http.MethodPost, "/change-sets/set-1/publish", "id", "set-1", "",
	))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "closed without merge") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
	if len(fakeGH.mergeCalls) != 0 {
		t.Fatalf("merge calls = %d, want 0", len(fakeGH.mergeCalls))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}
