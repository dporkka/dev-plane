package verifier

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	repoprotocol "github.com/ai-dev-control-plane/repoprotocol"
)

type ciSourceStub struct {
	record repoprotocol.RemoteCIRecord
	err    error
	called int
	query  RemoteCIQuery
}

func (s *ciSourceStub) FetchAttempt(_ context.Context, query RemoteCIQuery) (repoprotocol.RemoteCIRecord, error) {
	s.called++
	s.query = query
	return s.record, s.err
}

func remoteCISetup(t *testing.T) (RemoteCIAttestor, RemoteCIQuery, *ciSourceStub) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("immutable-ci-definition"))
	head := strings.Repeat("a", 40)
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	q := RemoteCIQuery{HeadSHA: head, PipelineID: "build-1", RunID: "run-1", JobID: "test", AttemptID: "attempt-7"}
	record := repoprotocol.RemoteCIRecord{
		Version: 1, Provider: "woodpecker", Repository: "dporkka/dev-plane", HeadSHA: head,
		PipelineID: q.PipelineID, DefinitionSHA256: hex.EncodeToString(digest[:]),
		RunID: q.RunID, JobID: q.JobID, AttemptID: q.AttemptID, WorkerID: "runner-4",
		State: repoprotocol.CIStateTerminal, Outcome: repoprotocol.CIOutcomePassed, ExitCode: ciCode(0),
		QueuedAt: now, AssignedAt: now.Add(time.Second), StartedAt: now.Add(2 * time.Second), FinishedAt: now.Add(3 * time.Second),
	}
	src := &ciSourceStub{record: record}
	return RemoteCIAttestor{Source: src, Policy: repoprotocol.RemoteCIPolicy{
		Provider: "woodpecker", Repository: "dporkka/dev-plane",
		DefinitionSHA256: record.DefinitionSHA256, SignerKeyID: "attestor-1", PublicKey: pub,
	}, PrivateKey: priv}, q, src
}
func ciCode(n int) *int { return &n }

func TestAttestRemoteCIRequiresAuthoritativeExactAttempt(t *testing.T) {
	attestor, query, src := remoteCISetup(t)
	receipt, err := attestor.Attest(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if src.called != 1 || src.query != query {
		t.Fatalf("wrong authoritative query: %+v", src.query)
	}
	if err := receipt.Verify(query.HeadSHA, attestor.Policy); err != nil {
		t.Fatalf("signed proof invalid: %v", err)
	}
}

func TestAttestRemoteCIRejectsMismatchedProviderFacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*repoprotocol.RemoteCIRecord)
	}{
		{"head", func(r *repoprotocol.RemoteCIRecord) { r.HeadSHA = strings.Repeat("f", 40) }},
		{"pipeline", func(r *repoprotocol.RemoteCIRecord) { r.PipelineID = "other" }},
		{"run", func(r *repoprotocol.RemoteCIRecord) { r.RunID = "other" }},
		{"job", func(r *repoprotocol.RemoteCIRecord) { r.JobID = "other" }},
		{"attempt", func(r *repoprotocol.RemoteCIRecord) { r.AttemptID = "other" }},
		{"definition", func(r *repoprotocol.RemoteCIRecord) { r.DefinitionSHA256 = strings.Repeat("f", 64) }},
		{"provider", func(r *repoprotocol.RemoteCIRecord) { r.Provider = "buildkite" }},
		{"no runner", func(r *repoprotocol.RemoteCIRecord) { r.WorkerID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attestor, query, src := remoteCISetup(t)
			tc.mutate(&src.record)
			receipt, err := attestor.Attest(context.Background(), query)
			if err == nil || receipt.Signature != "" {
				t.Fatalf("accepted mismatch: %+v", receipt)
			}
		})
	}
}

func TestAttestRemoteCIDoesNotTurnProviderFailureIntoSuccess(t *testing.T) {
	attestor, query, src := remoteCISetup(t)
	src.err = errors.New("CI server unavailable")
	receipt, err := attestor.Attest(context.Background(), query)
	if err == nil || receipt.Signature != "" {
		t.Fatalf("provider outage falsely attested: %+v, %v", receipt, err)
	}
}

func TestAttestRemoteCIRejectsIncorrectSigningKey(t *testing.T) {
	attestor, query, src := remoteCISetup(t)
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	attestor.PrivateKey = other
	receipt, err := attestor.Attest(context.Background(), query)
	if err == nil || receipt.Signature != "" || src.called != 0 {
		t.Fatal("untrusted signing key used")
	}
}

func TestAttestRemoteCIPendingReceiptCannotPassReleaseGate(t *testing.T) {
	attestor, query, src := remoteCISetup(t)
	src.record.State = repoprotocol.CIStateQueued
	src.record.WorkerID = ""
	src.record.Outcome = ""
	src.record.ExitCode = nil
	src.record.AssignedAt = time.Time{}
	src.record.StartedAt = time.Time{}
	src.record.FinishedAt = time.Time{}
	receipt, err := attestor.Attest(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if err := receipt.VerifyObservation(attestor.Policy); err != nil {
		t.Fatalf("signed queued observation should remain inspectable: %v", err)
	}
	if err := receipt.Verify(query.HeadSHA, attestor.Policy); err == nil {
		t.Fatal("queued job passed release gate")
	}
}
