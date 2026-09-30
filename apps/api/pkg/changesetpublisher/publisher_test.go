package changesetpublisher

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/oauth2"

	"github.com/ai-dev-control-plane/api/pkg/changeauthority"
	"github.com/ai-dev-control-plane/changegraph"
	"github.com/ai-dev-control-plane/changeset"
	"github.com/ai-dev-control-plane/decisionpacket"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
)

type fakeReader struct {
	results []*gateway.GitHubPR
	calls   int
	err     error
}

func (f *fakeReader) GetPR(ctx context.Context, token *oauth2.Token, owner, name string, number int) (*gateway.GitHubPR, error) {
	if f.err != nil {
		return nil, f.err
	}
	idx := f.calls
	if idx >= len(f.results) {
		idx = len(f.results) - 1
	}
	f.calls++
	if idx < 0 {
		return nil, nil
	}
	return f.results[idx], nil
}

type fakeMerger struct {
	calls []changeauthority.Request
	err   error
}

func (f *fakeMerger) Merge(ctx context.Context, actor changeauthority.Actor, req changeauthority.Request) (*models.PullRequest, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return nil, f.err
	}
	return &models.PullRequest{ID: req.PullRequestID, State: "merged"}, nil
}

type fakePublisher struct {
	subjects []string
	events   []events.ChangeSetPublicationEvent
}

func (f *fakePublisher) Publish(subject string, data []byte) error {
	f.subjects = append(f.subjects, subject)
	var event events.ChangeSetPublicationEvent
	_ = json.Unmarshal(data, &event)
	f.events = append(f.events, event)
	return nil
}

func remotePR(state string, merged bool, headSHA, mergeSHA string) *gateway.GitHubPR {
	pr := &gateway.GitHubPR{Number: 42, State: state, Merged: merged, MergeCommitSHA: mergeSHA}
	pr.Head.SHA = headSHA
	pr.Head.Ref = "feature"
	pr.Base.Ref = "main"
	return pr
}

func publicationFixture(t *testing.T, now time.Time) (decisionDigest, packet, setDigest, manifest string) {
	t.Helper()
	dp, err := decisionpacket.New(decisionpacket.Input{
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
		t.Fatalf("decisionpacket.New(): %v", err)
	}
	decisionDigest, err = dp.Digest()
	if err != nil {
		t.Fatalf("packet digest: %v", err)
	}
	raw, err := dp.Marshal()
	if err != nil {
		t.Fatalf("packet marshal: %v", err)
	}
	packet = string(raw)

	m, err := changeset.NewManifest(changeset.ManifestInput{
		ChangeSetID: "set-1",
		ProjectID:   "project-1",
		Members: []changeset.Member{{
			ID: "candidate-1", CommitSHA: "candidate-sha", TreeHash: "tree-a",
			DecisionDigest: decisionDigest, Approvable: true,
		}},
		Graph: changegraph.Graph{Nodes: []changegraph.Node{{ID: "candidate-1"}}},
	})
	if err != nil {
		t.Fatalf("changeset.NewManifest(): %v", err)
	}
	setDigest, err = m.Digest()
	if err != nil {
		t.Fatalf("manifest digest: %v", err)
	}
	manifestRaw, err := m.Marshal()
	if err != nil {
		t.Fatalf("manifest marshal: %v", err)
	}
	manifest = string(manifestRaw)
	return
}

func expectAuthoritySnapshot(t *testing.T, mock sqlmock.Sqlmock, now time.Time) {
	t.Helper()
	decisionDigest, packet, setDigest, manifest := publicationFixture(t, now)
	mock.ExpectQuery("SELECT cs.id, cs.project_id, cs.status, cs.publication_status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "project_id", "status", "publication_status", "publication_digest",
			"publication_manifest", "organization_id",
		}).AddRow("set-1", "project-1", "authorized", "pending", setDigest, manifest, "org-1"))
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
}

func expectExecutionPlan(mock sqlmock.Sqlmock) {
	mock.ExpectExec("UPDATE change_sets").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO change_set_publications").
		WithArgs("set-1", "candidate-1", 0, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT cp.candidate_id, cp.ordinal, cp.status").
		WithArgs("set-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"candidate_id", "ordinal", "status", "attempt_count", "merge_sha", "last_error",
			"pull_request_id", "commit_sha", "task_id", "pr_number", "owner", "name",
		}).AddRow("candidate-1", 0, "pending", 0, nil, nil, "pr-1", "candidate-sha", "task-1", 42, "owner", "repo"))
	mock.ExpectExec("UPDATE change_sets").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE change_set_publications").
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestPublishReconcilesAlreadyMergedRemoteMember(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New(): %v", err)
	}
	defer db.Close()
	now := time.Now().UTC()
	expectAuthoritySnapshot(t, mock, now)
	expectExecutionPlan(mock)
	mock.ExpectExec("UPDATE pull_requests SET state = 'merged'").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE tasks SET status = 'done'").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE change_set_publications SET status = 'merged'").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE change_sets").
		WillReturnResult(sqlmock.NewResult(0, 1))

	reader := &fakeReader{results: []*gateway.GitHubPR{remotePR("closed", true, "candidate-sha", "merge-sha")}}
	merger := &fakeMerger{}
	eventBus := &fakePublisher{}
	publisher := New(db, slog.Default()).
		WithGitHubReader(reader).
		WithMergeAuthority(merger).
		WithGitHubToken("token").
		WithEventPublisher(eventBus)

	result, err := publisher.Publish(context.Background(), "set-1", changeauthority.Actor{
		UserID: "user-1", OrganizationID: "org-1", Role: models.RoleOwner,
	}, nil)
	if err != nil {
		t.Fatalf("Publish() error: %v", err)
	}
	if result.PublicationStatus != "completed" {
		t.Fatalf("status = %q, want completed", result.PublicationStatus)
	}
	if len(merger.calls) != 0 {
		t.Fatalf("merge calls = %d, want 0", len(merger.calls))
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls = %d, want 1", reader.calls)
	}
	if len(eventBus.subjects) != 2 ||
		eventBus.subjects[0] != events.ChangeSetPublicationStarted ||
		eventBus.subjects[1] != events.ChangeSetPublicationCompleted {
		t.Fatalf("event subjects = %#v", eventBus.subjects)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet DB expectations: %v", err)
	}
}

func TestPublishOpenMemberUsesSharedMergeAuthorityAndConfirmsRemoteState(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New(): %v", err)
	}
	defer db.Close()
	now := time.Now().UTC()
	expectAuthoritySnapshot(t, mock, now)
	expectExecutionPlan(mock)
	mock.ExpectExec("UPDATE change_set_publications SET status = 'merged'").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE change_sets").
		WillReturnResult(sqlmock.NewResult(0, 1))

	reader := &fakeReader{results: []*gateway.GitHubPR{
		remotePR("open", false, "candidate-sha", ""),
		remotePR("closed", true, "candidate-sha", "merge-sha"),
	}}
	merger := &fakeMerger{}
	publisher := New(db, slog.Default()).
		WithGitHubReader(reader).
		WithMergeAuthority(merger).
		WithGitHubToken("token")

	result, err := publisher.Publish(context.Background(), "set-1", changeauthority.Actor{
		UserID: "user-1", OrganizationID: "org-1", Role: models.RoleOwner,
	}, nil)
	if err != nil {
		t.Fatalf("Publish() error: %v", err)
	}
	if result.PublicationStatus != "completed" {
		t.Fatalf("status = %q, want completed", result.PublicationStatus)
	}
	if len(merger.calls) != 1 || merger.calls[0].PullRequestID != "pr-1" {
		t.Fatalf("merge calls = %#v", merger.calls)
	}
	if reader.calls != 2 {
		t.Fatalf("reader calls = %d, want 2", reader.calls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet DB expectations: %v", err)
	}
}
