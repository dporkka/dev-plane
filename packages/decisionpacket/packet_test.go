package decisionpacket

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewBuildsDeterministicCandidateBoundPacket(t *testing.T) {
	createdAt := time.Date(2026, time.September, 30, 14, 30, 0, 0, time.UTC)
	input := Input{
		Candidate: Candidate{
			ID:            "candidate-1",
			PullRequestID: "pr-1",
			TaskID:        "task-1",
			RunID:         "run-1",
			RepositoryID:  "repo-1",
			CommitSHA:     "commit-a",
			TreeHash:      "tree-a",
			Branch:        "agent/task-1",
		},
		Task: TaskSnapshot{
			Title:                "Ship trustworthy changes",
			Description:          "Bind approval to exact bytes.",
			AcceptanceCriteria:   json.RawMessage(`["tests pass","merge pins sha"]`),
			ApprovalRequirements: json.RawMessage(`["owner"]`),
		},
		Review: ReviewSnapshot{
			Summary:       "Ready after automated review.",
			RiskLevel:     "low",
			Approvable:    true,
			TestCoverage:  "focused verification",
			SecurityNotes: "No blocking findings.",
			Findings:      json.RawMessage(`[]`),
			DiffSummary:   json.RawMessage(`{"files_changed":3}`),
		},
		Verification: VerificationSnapshot{
			ContractHash:      "contract-a",
			EnvironmentDigest: "env-a",
			RunnerIdentity:    "runtime:runner-1",
			Checks:            json.RawMessage(`[{"id":"unit","passed":true,"exit_code":0}]`),
		},
		CreatedAt: createdAt,
	}

	first, err := New(input)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	second, err := New(input)
	if err != nil {
		t.Fatalf("New() second error: %v", err)
	}

	firstDigest, err := first.Digest()
	if err != nil {
		t.Fatalf("Digest() error: %v", err)
	}
	secondDigest, err := second.Digest()
	if err != nil {
		t.Fatalf("Digest() second error: %v", err)
	}
	if firstDigest == "" || firstDigest != secondDigest {
		t.Fatalf("digests = %q and %q, want equal non-empty digests", firstDigest, secondDigest)
	}
	if first.Version != PacketVersion {
		t.Fatalf("version = %d, want %d", first.Version, PacketVersion)
	}
	if first.Candidate.CommitSHA != "commit-a" || first.Candidate.TreeHash != "tree-a" {
		t.Fatalf("candidate = %#v", first.Candidate)
	}
}

func TestDigestChangesWhenDecisionMaterialChanges(t *testing.T) {
	packet := validPacket(t)
	original, err := packet.Digest()
	if err != nil {
		t.Fatalf("Digest() error: %v", err)
	}

	packet.Review.RiskLevel = "high"
	changed, err := packet.Digest()
	if err != nil {
		t.Fatalf("Digest() after mutation error: %v", err)
	}
	if original == changed {
		t.Fatal("digest did not change after review risk changed")
	}
}

func TestValidateRejectsIncompleteAuthorityIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Packet)
	}{
		{"missing candidate id", func(p *Packet) { p.Candidate.ID = "" }},
		{"missing commit sha", func(p *Packet) { p.Candidate.CommitSHA = "" }},
		{"missing tree hash", func(p *Packet) { p.Candidate.TreeHash = "" }},
		{"missing contract hash", func(p *Packet) { p.Verification.ContractHash = "" }},
		{"missing environment digest", func(p *Packet) { p.Verification.EnvironmentDigest = "" }},
		{"missing runner identity", func(p *Packet) { p.Verification.RunnerIdentity = "" }},
		{"missing checks", func(p *Packet) { p.Verification.Checks = nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packet := validPacket(t)
			tt.mutate(&packet)
			if err := packet.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestNewCopiesRawSnapshots(t *testing.T) {
	acceptance := json.RawMessage(`["original"]`)
	checks := json.RawMessage(`[{"id":"unit","passed":true}]`)
	packet, err := New(Input{
		Candidate: Candidate{
			ID: "candidate-1", PullRequestID: "pr-1", TaskID: "task-1", RunID: "run-1",
			RepositoryID: "repo-1", CommitSHA: "commit-a", TreeHash: "tree-a", Branch: "agent/task",
		},
		Task: TaskSnapshot{Title: "Task", AcceptanceCriteria: acceptance},
		Review: ReviewSnapshot{RiskLevel: "low", Approvable: true},
		Verification: VerificationSnapshot{
			ContractHash: "contract-a", EnvironmentDigest: "env-a", RunnerIdentity: "runtime:runner-1", Checks: checks,
		},
		CreatedAt: time.Date(2026, time.September, 30, 14, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	acceptance[2] = 'X'
	checks[2] = 'X'
	if string(packet.Task.AcceptanceCriteria) != `["original"]` {
		t.Fatalf("acceptance criteria mutated through input alias: %s", packet.Task.AcceptanceCriteria)
	}
	if string(packet.Verification.Checks) != `[{"id":"unit","passed":true}]` {
		t.Fatalf("checks mutated through input alias: %s", packet.Verification.Checks)
	}
}

func validPacket(t *testing.T) Packet {
	t.Helper()
	packet, err := New(Input{
		Candidate: Candidate{
			ID: "candidate-1", PullRequestID: "pr-1", TaskID: "task-1", RunID: "run-1",
			RepositoryID: "repo-1", CommitSHA: "commit-a", TreeHash: "tree-a", Branch: "agent/task",
		},
		Task: TaskSnapshot{Title: "Task"},
		Review: ReviewSnapshot{RiskLevel: "low", Approvable: true},
		Verification: VerificationSnapshot{
			ContractHash: "contract-a", EnvironmentDigest: "env-a", RunnerIdentity: "runtime:runner-1",
			Checks: json.RawMessage(`[{"id":"unit","passed":true,"exit_code":0}]`),
		},
		CreatedAt: time.Date(2026, time.September, 30, 14, 30, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return packet
}
