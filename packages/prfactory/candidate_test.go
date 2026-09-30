package prfactory

import (
	"context"
	"encoding/json"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/decisionpacket"
	"github.com/ai-dev-control-plane/models"
)

func TestLoadLatestVerificationEvidenceRequiresExactCandidateTree(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	defer db.Close()

	completed := time.Date(2026, time.September, 30, 13, 0, 0, 0, time.UTC)
	output := `{
		"source":"verification_contract",
		"passed":true,
		"tree_hash":"tree-a",
		"contract_hash":"contract-a",
		"environment_digest":"env-a",
		"runner_identity":"runtime:runner-1",
		"checks":[{"id":"unit","passed":true,"exit_code":0}],
		"started_at":"2026-09-30T12:59:00Z",
		"completed_at":"2026-09-30T13:00:00Z"
	}`

	mock.ExpectQuery("SELECT tool_output").
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"tool_output"}).AddRow(output))

	factory := NewFactory(db, nil)
	evidence, err := factory.loadLatestVerificationEvidence(context.Background(), "run-1", "tree-a")
	if err != nil {
		t.Fatalf("loadLatestVerificationEvidence() error: %v", err)
	}
	if evidence.TreeHash != "tree-a" || evidence.ContractHash != "contract-a" {
		t.Fatalf("evidence = %#v", evidence)
	}
	if evidence.EnvironmentDigest != "env-a" || evidence.RunnerIdentity != "runtime:runner-1" {
		t.Fatalf("evidence identity = %#v", evidence)
	}
	if !evidence.CompletedAt.Equal(completed) {
		t.Fatalf("completed_at = %s, want %s", evidence.CompletedAt, completed)
	}
}

func TestLoadLatestVerificationEvidenceSkipsNewerNonContractTestStep(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	defer db.Close()

	smoke := `{"passed":true,"total":1,"source":"explicit_test"}`
	contract := `{
		"source":"verification_contract",
		"passed":true,
		"tree_hash":"tree-a",
		"contract_hash":"contract-a",
		"environment_digest":"env-a",
		"runner_identity":"runtime:runner-1",
		"checks":[{"id":"unit","passed":true,"exit_code":0}],
		"started_at":"2026-09-30T12:59:00Z",
		"completed_at":"2026-09-30T13:00:00Z"
	}`

	mock.ExpectQuery("SELECT tool_output").
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"tool_output"}).
			AddRow(smoke).
			AddRow(contract))

	factory := NewFactory(db, nil)
	evidence, err := factory.loadLatestVerificationEvidence(context.Background(), "run-1", "tree-a")
	if err != nil {
		t.Fatalf("loadLatestVerificationEvidence() error: %v", err)
	}
	if evidence.ContractHash != "contract-a" || evidence.TreeHash != "tree-a" {
		t.Fatalf("evidence = %#v", evidence)
	}
}

func TestLoadLatestVerificationEvidenceRejectsStaleTree(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	defer db.Close()

	output := `{
		"source":"verification_contract",
		"passed":true,
		"tree_hash":"tree-old",
		"contract_hash":"contract-a",
		"environment_digest":"env-a",
		"runner_identity":"runtime:runner-1",
		"checks":[{"id":"unit","passed":true,"exit_code":0}],
		"started_at":"2026-09-30T12:59:00Z",
		"completed_at":"2026-09-30T13:00:00Z"
	}`

	mock.ExpectQuery("SELECT tool_output").
		WithArgs("run-1").
		WillReturnRows(sqlmock.NewRows([]string{"tool_output"}).AddRow(output))

	factory := NewFactory(db, nil)
	if _, err := factory.loadLatestVerificationEvidence(context.Background(), "run-1", "tree-current"); err == nil {
		t.Fatal("loadLatestVerificationEvidence() error = nil, want stale tree rejection")
	}
}

func TestLoadLatestVerificationEvidenceRejectsMissingEvidence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT tool_output").
		WithArgs("run-1").
		WillReturnError(sql.ErrNoRows)

	factory := NewFactory(db, nil)
	if _, err := factory.loadLatestVerificationEvidence(context.Background(), "run-1", "tree-a"); err == nil {
		t.Fatal("loadLatestVerificationEvidence() error = nil, want missing evidence rejection")
	}
}


func TestRecordPullRequestAllowsReviewWithoutVerificationEvidence(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, time.September, 30, 13, 30, 0, 0, time.UTC)
	pr := testPullRequest(now)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO pull_requests").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE tasks SET status = 'pr_created'").
		WithArgs(now, pr.TaskID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	factory := NewFactory(db, nil)
	if err := factory.recordPullRequest(context.Background(), pr); err != nil {
		t.Fatalf("recordPullRequest() error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestRecordVerifiedPullRequestPersistsCandidateAndEvidenceAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, time.September, 30, 13, 30, 0, 0, time.UTC)
	pr := testPullRequest(now)
	workspaceID := "workspace-1"
	candidate := verifiedCandidateRecord{
		ID:            "candidate-1",
		PullRequestID: pr.ID,
		TaskID:        pr.TaskID,
		RunID:         "run-1",
		WorkspaceID:   &workspaceID,
		RepositoryID:  pr.RepoID,
		CommitSHA:     "commit-a",
		TreeHash:      "tree-a",
		Branch:        pr.Branch,
		CreatedAt:     now,
	}
	evidence := &verificationEvidenceRecord{
		ID:                "evidence-1",
		CandidateID:       candidate.ID,
		TreeHash:          "tree-a",
		ContractHash:      "contract-a",
		EnvironmentDigest: "env-a",
		RunnerIdentity:    "runtime:runner-1",
		Checks:            json.RawMessage(`[{"id":"unit","passed":true,"exit_code":0}]`),
		StartedAt:         now.Add(-time.Minute),
		CompletedAt:       now,
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO pull_requests").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO change_candidates").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO verification_evidence").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO decision_packets").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE tasks SET status = 'pr_created'").
		WithArgs(now, pr.TaskID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	packet := mustDecisionPacketRecord(t, pr, candidate, evidence)
	factory := NewFactory(db, nil)
	if err := factory.recordVerifiedPullRequest(context.Background(), pr, candidate, evidence, packet); err != nil {
		t.Fatalf("recordVerifiedPullRequest() error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet database expectations: %v", err)
	}
}

func TestRecordVerifiedPullRequestRejectsTreeMismatchBeforeTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, time.September, 30, 13, 30, 0, 0, time.UTC)
	pr := testPullRequest(now)
	candidate := verifiedCandidateRecord{
		ID:            "candidate-1",
		PullRequestID: pr.ID,
		TaskID:        pr.TaskID,
		RunID:         "run-1",
		RepositoryID:  pr.RepoID,
		CommitSHA:     "commit-a",
		TreeHash:      "tree-current",
		Branch:        pr.Branch,
		CreatedAt:     now,
	}
	evidence := &verificationEvidenceRecord{
		ID:                "evidence-1",
		CandidateID:       candidate.ID,
		TreeHash:          "tree-verified",
		ContractHash:      "contract-a",
		EnvironmentDigest: "env-a",
		RunnerIdentity:    "runtime:runner-1",
		Checks:            json.RawMessage(`[{"id":"unit","passed":true,"exit_code":0}]`),
		StartedAt:         now.Add(-time.Minute),
		CompletedAt:       now,
	}

	packet := mustDecisionPacketRecord(t, pr, candidate, evidence)
	factory := NewFactory(db, nil)
	if err := factory.recordVerifiedPullRequest(context.Background(), pr, candidate, evidence, packet); err == nil {
		t.Fatal("recordVerifiedPullRequest() error = nil, want tree mismatch rejection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected database activity: %v", err)
	}
}

func mustDecisionPacketRecord(t *testing.T, pr *models.PullRequest, candidate verifiedCandidateRecord, evidence *verificationEvidenceRecord) decisionPacketRecord {
	t.Helper()
	packet, err := decisionpacket.New(decisionpacket.Input{
		Candidate: decisionpacket.Candidate{
			ID: candidate.ID, PullRequestID: pr.ID, TaskID: pr.TaskID, RunID: candidate.RunID,
			RepositoryID: pr.RepoID, CommitSHA: candidate.CommitSHA, TreeHash: candidate.TreeHash, Branch: candidate.Branch,
		},
		Task: decisionpacket.TaskSnapshot{Title: "Verified change"},
		Review: decisionpacket.ReviewSnapshot{RiskLevel: "low", Approvable: true},
		Verification: decisionpacket.VerificationSnapshot{
			ContractHash: evidence.ContractHash,
			EnvironmentDigest: evidence.EnvironmentDigest,
			RunnerIdentity: evidence.RunnerIdentity,
			Checks: evidence.Checks,
		},
		CreatedAt: candidate.CreatedAt,
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
	return decisionPacketRecord{
		ID: "packet-1", CandidateID: candidate.ID, Digest: digest, Packet: data, CreatedAt: candidate.CreatedAt,
	}
}

func testPullRequest(now time.Time) *models.PullRequest {
	runID := "run-1"
	return &models.PullRequest{
		ID:         "pr-1",
		TaskID:     "task-1",
		RunID:      &runID,
		RepoID:     "repo-1",
		Number:     42,
		Title:      "Verified change",
		Body:       "body",
		Branch:     "agent/task-1",
		BaseBranch: "main",
		URL:        "https://github.com/owner/repo/pull/42",
		State:      models.PRStateOpen,
		CreatedBy:  "user-1",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}
