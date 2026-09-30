package prfactory

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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
