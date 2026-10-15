package db

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	protocol "github.com/ai-dev-control-plane/repoprotocol"
)

func storedVerificationAttempt() protocol.VerificationAttempt {
	candidate := strings.Repeat("c", 40)
	return protocol.VerificationAttempt{
		ID: "attempt-0001", WorkItemID: "work-0001",
		Repository: "dporkka/dev-plane", TargetBranch: "main",
		SourceHeadSHA: strings.Repeat("a", 40), TargetBaseSHA: strings.Repeat("b", 40),
		CandidateCommitSHA: candidate, CandidateTreeSHA: strings.Repeat("d", 40),
		PolicyDigest:  "sha256:" + strings.Repeat("e", 64),
		RunnerDigest:  "sha256:" + strings.Repeat("f", 64),
		RequiredGates: []string{"unit"},
		Gates: []protocol.VerificationGate{{
			Name: "unit", SubjectSHA: candidate, Status: protocol.GatePassed,
			LogDigest: "sha256:" + strings.Repeat("1", 64),
		}},
		CompletedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
	}
}

func TestVerificationAttemptStoreIsAppendOnlyByID(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RunMigrations("migrations"); err != nil {
		t.Fatal(err)
	}

	original := storedVerificationAttempt()
	if err := db.InsertVerificationAttempt(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	altered := original
	altered.PolicyDigest = "sha256:" + strings.Repeat("9", 64)
	if err := db.InsertVerificationAttempt(context.Background(), altered); err == nil {
		t.Fatal("duplicate attempt ID must not overwrite existing evidence")
	}
	// Even direct SQL writers must not be able to rewrite or delete concluded evidence.
	if _, err := db.Exec("UPDATE verification_attempts SET policy_digest = ? WHERE id = ?", "forged", original.ID); err == nil {
		t.Fatal("direct SQL UPDATE changed immutable evidence")
	}
	if _, err := db.Exec("DELETE FROM verification_attempts WHERE id = ?", original.ID); err == nil {
		t.Fatal("direct SQL DELETE removed immutable evidence")
	}
	stored, err := db.GetVerificationAttempt(context.Background(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PolicyDigest != original.PolicyDigest || stored.CandidateCommitSHA != original.CandidateCommitSHA {
		t.Fatalf("original attempt overwritten: %+v", stored)
	}
}

func TestVerificationAttemptStoreRejectsIncompleteEvidence(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RunMigrations("migrations"); err != nil {
		t.Fatal(err)
	}

	invalid := storedVerificationAttempt()
	invalid.CandidateTreeSHA = ""
	if err := db.InsertVerificationAttempt(context.Background(), invalid); err == nil {
		t.Fatal("incomplete evidence must never be persisted as a concluded attempt")
	}
}

func TestVerificationAttemptPostgresImmutableAgainstDirectSQL(t *testing.T) {
	url := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL required")
	}
	database, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.RunMigrations("migrations"); err != nil {
		t.Fatal(err)
	}
	a := storedVerificationAttempt()
	a.ID = fmt.Sprintf("attempt-%d", time.Now().UnixNano())
	if err := database.InsertVerificationAttempt(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE verification_attempts SET policy_digest = $1 WHERE id = $2", "forged", a.ID); err == nil {
		t.Fatal("PostgreSQL direct UPDATE bypassed immutable evidence")
	}
	if _, err := database.Exec("DELETE FROM verification_attempts WHERE id = $1", a.ID); err == nil {
		t.Fatal("PostgreSQL direct DELETE bypassed immutable evidence")
	}
	stored, err := database.GetVerificationAttempt(context.Background(), a.ID)
	if err != nil || stored.PolicyDigest != a.PolicyDigest {
		t.Fatalf("attempt changed after forbidden SQL: %v %+v", err, stored)
	}
}
