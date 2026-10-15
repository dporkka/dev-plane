package db

import (
    "context"
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
        PolicyDigest: "sha256:" + strings.Repeat("e", 64),
        RunnerDigest: "sha256:" + strings.Repeat("f", 64),
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
    if err != nil { t.Fatal(err) }
    defer db.Close()
    if err := db.RunMigrations("migrations"); err != nil { t.Fatal(err) }

    original := storedVerificationAttempt()
    if err := db.InsertVerificationAttempt(context.Background(), original); err != nil { t.Fatal(err) }
    altered := original
    altered.PolicyDigest = "sha256:" + strings.Repeat("9", 64)
    if err := db.InsertVerificationAttempt(context.Background(), altered); err == nil {
        t.Fatal("duplicate attempt ID must not overwrite existing evidence")
    }
    stored, err := db.GetVerificationAttempt(context.Background(), original.ID)
    if err != nil { t.Fatal(err) }
    if stored.PolicyDigest != original.PolicyDigest || stored.CandidateCommitSHA != original.CandidateCommitSHA {
        t.Fatalf("original attempt overwritten: %+v", stored)
    }
}

func TestVerificationAttemptStoreRejectsIncompleteEvidence(t *testing.T) {
    db, err := New(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    if err := db.RunMigrations("migrations"); err != nil { t.Fatal(err) }

    invalid := storedVerificationAttempt()
    invalid.CandidateTreeSHA = ""
    if err := db.InsertVerificationAttempt(context.Background(), invalid); err == nil {
        t.Fatal("incomplete evidence must never be persisted as a concluded attempt")
    }
}
