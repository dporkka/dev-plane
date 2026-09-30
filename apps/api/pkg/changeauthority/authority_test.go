package changeauthority

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/decisionpacket"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
)

type fakeGateway struct {
	result *gateway.MergePRResult
	err    error
	calls  []gateway.MergePRRequest
}

func (f *fakeGateway) MergePR(ctx context.Context, token *oauth2.Token, owner, name string, number int, req gateway.MergePRRequest) (*gateway.MergePRResult, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

func allowAllKernel() *capability.Kernel {
	engine := policies.NewEngine([]policies.Policy{{
		Name: "allow_all_tests", ResourceType: "*", Action: "*", Effect: policies.EffectAllow,
	}})
	return capability.NewKernel(engine, nil, nil, slog.Default())
}

func decisionPacketFixture(t *testing.T, now time.Time) (string, string) {
	t.Helper()
	packet, err := decisionpacket.New(decisionpacket.Input{
		Candidate: decisionpacket.Candidate{
			ID: "candidate-1", PullRequestID: "pr-1", TaskID: "task-1", RunID: "run-1",
			RepositoryID: "repo-1", CommitSHA: "candidate-sha", TreeHash: "tree-a", Branch: "feature",
		},
		Task:   decisionpacket.TaskSnapshot{Title: "title"},
		Review: decisionpacket.ReviewSnapshot{RiskLevel: "low", Approvable: true},
		Verification: decisionpacket.VerificationSnapshot{
			ContractHash: "contract-a", EnvironmentDigest: "env-a", RunnerIdentity: "runtime:runner-1",
			Checks: json.RawMessage(`[{"id":"unit","passed":true,"exit_code":0}]`),
		},
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("decisionpacket.New() error: %v", err)
	}
	digest, err := packet.Digest()
	if err != nil {
		t.Fatalf("packet.Digest() error: %v", err)
	}
	raw, err := packet.Marshal()
	if err != nil {
		t.Fatalf("packet.Marshal() error: %v", err)
	}
	return digest, string(raw)
}

func expectMergeAuthorityBase(t *testing.T, mock sqlmock.Sqlmock, now time.Time) string {
	t.Helper()
	digest, packet := decisionPacketFixture(t, now)
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs("pr-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body",
			"branch", "base_branch", "url", "state", "draft", "created_by", "merged_at",
			"created_at", "updated_at", "owner", "name", "task_status", "organization_id",
		}).AddRow(
			"pr-1", "task-1", nil, "repo-1", 42, "title", "body",
			"feature", "main", "https://github.com/owner/repo/pull/42", "open", false, "user-1", nil,
			now, now, "owner", "repo", "pr_created", "org-1",
		))
	mock.ExpectQuery("SELECT c.commit_sha, c.tree_hash, e.tree_hash").
		WithArgs("pr-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"commit_sha", "candidate_tree_hash", "evidence_tree_hash", "contract_hash",
			"environment_digest", "runner_identity", "completed_at", "candidate_id",
			"packet_digest", "packet", "project_id",
		}).AddRow(
			"candidate-sha", "tree-a", "tree-a", "contract-a",
			"env-a", "runtime:runner-1", now, "candidate-1", digest, packet, "project-1",
		))
	mock.ExpectQuery("SELECT c.id, c.pull_request_id, c.repository_id, c.commit_sha, c.tree_hash").
		WithArgs("project-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pull_request_id", "repository_id", "commit_sha", "tree_hash", "state", "depends_on_candidate_id",
		}).AddRow("candidate-1", "pr-1", "repo-1", "candidate-sha", "tree-a", "open", nil))
	mock.ExpectQuery("SELECT cs.id, cs.project_id, cs.status, cs.publication_digest, cs.publication_manifest").
		WithArgs("candidate-1").
		WillReturnError(sql.ErrNoRows)
	return digest
}

func TestMergePinsVerifiedCandidateSHA(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New(): %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	expectMergeAuthorityBase(t, mock, now)
	mock.ExpectExec("UPDATE pull_requests SET state = 'merged'").
		WithArgs(sqlmock.AnyArg(), "pr-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE tasks SET status = 'done'").
		WithArgs(sqlmock.AnyArg(), "task-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	gh := &fakeGateway{result: &gateway.MergePRResult{Merged: true, SHA: "merge-sha"}}
	service := New(db, slog.Default()).
		WithGitHubGateway(gh).
		WithGitHubToken("token").
		WithCapabilityKernel(allowAllKernel())

	pr, err := service.Merge(context.Background(), Actor{
		UserID: "user-1", OrganizationID: "org-1", Role: models.RoleOwner,
	}, Request{PullRequestID: "pr-1"})
	if err != nil {
		t.Fatalf("Merge() error: %v", err)
	}
	if pr.State != "merged" {
		t.Fatalf("pr state = %q, want merged", pr.State)
	}
	if len(gh.calls) != 1 || gh.calls[0].SHA != "candidate-sha" {
		t.Fatalf("gateway calls = %#v", gh.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet DB expectations: %v", err)
	}
}

func TestMergeRejectsCallerSHADifferentFromVerifiedCandidate(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New(): %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	expectMergeAuthorityBase(t, mock, now)
	gh := &fakeGateway{result: &gateway.MergePRResult{Merged: true, SHA: "merge-sha"}}
	service := New(db, slog.Default()).
		WithGitHubGateway(gh).
		WithGitHubToken("token").
		WithCapabilityKernel(allowAllKernel())

	_, err = service.Merge(context.Background(), Actor{
		UserID: "user-1", OrganizationID: "org-1", Role: models.RoleOwner,
	}, Request{PullRequestID: "pr-1", SHA: "other-sha"})
	if StatusCode(err) != 409 {
		t.Fatalf("StatusCode(error) = %d, want 409; error = %v", StatusCode(err), err)
	}
	if len(gh.calls) != 0 {
		t.Fatalf("merge calls = %d, want 0", len(gh.calls))
	}
}

func TestMergeRejectsActorFromDifferentOrganization(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New(): %v", err)
	}
	defer db.Close()

	now := time.Now().UTC()
	mock.ExpectQuery("SELECT pr.id, pr.task_id, pr.run_id").
		WithArgs("pr-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "run_id", "repository_id", "number", "title", "body",
			"branch", "base_branch", "url", "state", "draft", "created_by", "merged_at",
			"created_at", "updated_at", "owner", "name", "task_status", "organization_id",
		}).AddRow(
			"pr-1", "task-1", nil, "repo-1", 42, "title", "body",
			"feature", "main", "https://github.com/owner/repo/pull/42", "open", false, "user-1", nil,
			now, now, "owner", "repo", "pr_created", "org-1",
		))

	service := New(db, slog.Default()).
		WithGitHubGateway(&fakeGateway{}).
		WithGitHubToken("token").
		WithCapabilityKernel(allowAllKernel())
	_, err = service.Merge(context.Background(), Actor{
		UserID: "user-2", OrganizationID: "org-2", Role: models.RoleOwner,
	}, Request{PullRequestID: "pr-1"})
	if StatusCode(err) != 404 {
		t.Fatalf("StatusCode(error) = %d, want 404; error = %v", StatusCode(err), err)
	}
}
