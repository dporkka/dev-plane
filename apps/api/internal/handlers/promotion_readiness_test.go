package handlers

import (
	"context"
	"encoding/json"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
	promotion "github.com/ai-dev-control-plane/promotion"
	gh "github.com/ai-dev-control-plane/promotion/github"
	protocol "github.com/ai-dev-control-plane/repoprotocol"
)

type fakePromotionReader struct {
	observation gh.Observation
	err         error
	called      bool
}

func (f *fakePromotionReader) Observe(_ context.Context, repo string, number int) (gh.Observation, error) {
	f.called = true
	if repo != "acme/widget" || number != 42 {
		panic("wrong repository or PR")
	}
	return f.observation, f.err
}
func readinessRequest(id, attempt string) *http.Request {
	req := httptest.NewRequest("GET", "/pull-requests/"+id+"/promotion-readiness?attempt_id="+attempt, nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: testUserID, OrgID: testOrgID, Role: "owner"}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}
func testAttempt() protocol.VerificationAttempt {
	attempt := protocol.VerificationAttempt{
		ID: "attempt-1", WorkItemID: "task-1", Repository: "acme/widget", TargetBranch: "main",
		SourceHeadSHA: strings.Repeat("a", 40), TargetBaseSHA: strings.Repeat("b", 40),
		CandidateCommitSHA: strings.Repeat("c", 40), CandidateTreeSHA: strings.Repeat("d", 40),
		PolicyDigest: "sha256:" + strings.Repeat("e", 64), RunnerDigest: "sha256:" + strings.Repeat("f", 64),
		RequiredGates: []string{"unit"}, Gates: []protocol.VerificationGate{{Name: "unit", Status: protocol.GatePassed, SubjectSHA: strings.Repeat("c", 40), LogDigest: "sha256:" + strings.Repeat("1", 64)}},
		CompletedAt: time.Date(2026, 10, 15, 13, 0, 0, 0, time.UTC),
	}
	signed, err := protocol.SignVerificationAttempt(attempt, []byte(strings.Repeat("s",32)))
	if err != nil {panic(err)}
	return signed
}
func configureAttestationTest(t *testing.T) {
	t.Helper()
	t.Setenv("PROMOTION_ATTESTATION_KEY_BASE64", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("s",32))))
}
func goodObservation() gh.Observation {
	a := testAttempt()
	return gh.Observation{
		Snapshot: promotion.Snapshot{
			Repository: a.Repository, TargetBranch: a.TargetBranch,
			CurrentSourceHeadSHA: a.SourceHeadSHA, CurrentTargetHeadSHA: a.TargetBaseSHA,
			CandidateCommitSHA: a.CandidateCommitSHA, CandidateTreeSHA: a.CandidateTreeSHA,
		},
		Policy:       promotion.Policy{Digest: a.PolicyDigest, RequiredGates: []string{"unit"}, RequiredHumanApprovals: 1},
		Approvals:    []promotion.Approval{{ReviewerID: "trusted-reviewer", Approved: true, Human: true, CandidateCommitSHA: a.CandidateCommitSHA, PolicyDigest: a.PolicyDigest}},
		AgentActorID: "agent-user", PolicyRevisionSHA: a.TargetBaseSHA,
	}
}
func expectPromotionPR(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT p.organization_id").WithArgs("pr-1").
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(testOrgID))
	mock.ExpectQuery("SELECT pr.number, r.owner, r.name, pr.task_id, pr.state").
		WithArgs("pr-1").WillReturnRows(sqlmock.NewRows([]string{"number", "owner", "name", "task_id", "state"}).
		AddRow(42, "acme", "widget", "task-1", "open"))
}
func assertDecision(t *testing.T, body []byte, eligible bool, blocker string) {
	t.Helper()
	var out struct {
		Decision struct {
			Eligible bool     `json:"eligible"`
			DryRun   bool     `json:"dry_run"`
			Blockers []string `json:"blockers"`
		} `json:"decision"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if out.Decision.Eligible != eligible || !out.Decision.DryRun {
		t.Fatalf("wrong dry-run eligibility %s", body)
	}
	if blocker != "" {
		for _, b := range out.Decision.Blockers {
			if b == blocker {
				return
			}
		}
		t.Fatalf("missing blocker %s: %s", blocker, body)
	}
}
func TestPromotionReadinessEvaluatesStoredAttemptAndProtectedObservation(t *testing.T) {
	configureAttestationTest(t)
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectPromotionPR(mock)
	a := testAttempt()
	raw, _ := json.Marshal(a)
	mock.ExpectQuery("SELECT payload FROM verification_attempts").
		WithArgs(a.ID, a.WorkItemID, a.Repository).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(raw))
	reader := &fakePromotionReader{observation: goodObservation()}
	h.WithPromotionObserver(reader)
	rec := httptest.NewRecorder()
	h.GetPullRequestPromotionReadiness(rec, readinessRequest("pr-1", "attempt-1"))
	if rec.Code != 200 || !reader.called {
		t.Fatalf("status %d, reader=%v: %s", rec.Code, reader.called, rec.Body)
	}
	assertDecision(t, rec.Body.Bytes(), true, "")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestPromotionReadinessRejectsStaleCandidate(t *testing.T) {
	configureAttestationTest(t)
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectPromotionPR(mock)
	a := testAttempt()
	raw, _ := json.Marshal(a)
	mock.ExpectQuery("SELECT payload FROM verification_attempts").
		WithArgs(a.ID, a.WorkItemID, a.Repository).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(raw))
	obs := goodObservation()
	obs.Snapshot.CurrentTargetHeadSHA = strings.Repeat("9", 40)
	obs.PolicyRevisionSHA = obs.Snapshot.CurrentTargetHeadSHA // A new protected target may contain identical policy bytes.
	h.WithPromotionObserver(&fakePromotionReader{observation: obs})
	rec := httptest.NewRecorder()
	h.GetPullRequestPromotionReadiness(rec, readinessRequest("pr-1", "attempt-1"))
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	assertDecision(t, rec.Body.Bytes(), false, "stale_target")
}
func TestPromotionReadinessMissingAttemptFailsClosedWithoutCallingGitHub(t *testing.T) {
	configureAttestationTest(t)
	h, mock, cleanup := setupTest(t)
	defer cleanup()
	expectPromotionPR(mock)
	mock.ExpectQuery("SELECT payload FROM verification_attempts").
		WithArgs("missing", "task-1", "acme/widget").WillReturnRows(sqlmock.NewRows([]string{"payload"}))
	reader := &fakePromotionReader{observation: goodObservation()}
	h.WithPromotionObserver(reader)
	rec := httptest.NewRecorder()
	h.GetPullRequestPromotionReadiness(rec, readinessRequest("pr-1", "missing"))
	if rec.Code != 200 || reader.called {
		t.Fatalf("wrong missing attempt behavior: %d %s", rec.Code, rec.Body)
	}
	assertDecision(t, rec.Body.Bytes(), false, "missing_attempt")
}
func TestPromotionReadinessCannotBeUsedByUnauthenticatedCaller(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()
	req := httptest.NewRequest("GET", "/pull-requests/pr-1/promotion-readiness?attempt_id=x", nil)
	rec := httptest.NewRecorder()
	h.GetPullRequestPromotionReadiness(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestPromotionReadinessRejectsTamperedSignedEvidenceBeforeGitHub(t *testing.T) {
	configureAttestationTest(t)
	h,mock,cleanup:=setupTest(t)
	defer cleanup()
	expectPromotionPR(mock)
	a:=testAttempt()
	a.PolicyDigest="sha256:"+strings.Repeat("9",64)
	raw,_:=json.Marshal(a)
	mock.ExpectQuery("SELECT payload FROM verification_attempts").
		WithArgs(a.ID,a.WorkItemID,a.Repository).
		WillReturnRows(sqlmock.NewRows([]string{"payload"}).AddRow(raw))
	reader:=&fakePromotionReader{observation:goodObservation()}
	h.WithPromotionObserver(reader)
	rec:=httptest.NewRecorder()
	h.GetPullRequestPromotionReadiness(rec,readinessRequest("pr-1",a.ID))
	if rec.Code!=200||reader.called {t.Fatalf("tampered evidence reached observer: %s",rec.Body)}
	assertDecision(t,rec.Body.Bytes(),false,"invalid_evidence")
}
func TestPromotionReadinessRejectsMissingAttestationKeyBeforeQuery(t *testing.T) {
	t.Setenv("PROMOTION_ATTESTATION_KEY_BASE64","")
	h,mock,cleanup:=setupTest(t)
	defer cleanup()
	expectPromotionPR(mock)
	rec:=httptest.NewRecorder()
	h.GetPullRequestPromotionReadiness(rec,readinessRequest("pr-1","attempt-1"))
	if rec.Code!=200 {t.Fatalf("status %d: %s",rec.Code,rec.Body)}
	assertDecision(t,rec.Body.Bytes(),false,"invalid_evidence")
}
