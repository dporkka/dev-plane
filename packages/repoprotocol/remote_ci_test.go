package repoprotocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func testRemoteCI(t *testing.T) (RemoteCIReceipt, RemoteCIPolicy) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	source := strings.Repeat("a", 40)
	definitionSum := sha256.Sum256([]byte("definition"))
	artifactSum := sha256.Sum256([]byte("artifact"))
	def := hex.EncodeToString(definitionSum[:])
	artifact := hex.EncodeToString(artifactSum[:])
	record := RemoteCIRecord{
		Version: 1, Provider: "woodpecker", Repository: "dporkka/dev-plane", HeadSHA: source,
		PipelineID: "pipeline-32", DefinitionSHA256: def, RunID: "run-33", JobID: "job-1",
		AttemptID: "attempt-1", WorkerID: "worker-22", State: CIStateTerminal,
		Outcome: CIOutcomePassed, ExitCode: codePtr(0),
		QueuedAt: now, AssignedAt: now.Add(time.Second), StartedAt: now.Add(2 * time.Second),
		FinishedAt: now.Add(3 * time.Second),
		Artifacts:  []CIArtifact{{Name: "test-results.xml", SHA256: artifact, SizeBytes: 42}},
	}
	receipt, err := SignRemoteCIReceipt(record, "attestor-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	return receipt, RemoteCIPolicy{Provider: "woodpecker", Repository: "dporkka/dev-plane",
		DefinitionSHA256: def, SignerKeyID: "attestor-1", PublicKey: pub}
}

func codePtr(n int) *int { return &n }

func TestRemoteCIProofPassesOnlyWithPinnedSignerAndExactSource(t *testing.T) {
	receipt, policy := testRemoteCI(t)
	bundle := EvidenceBundle{BaseSHA: strings.Repeat("b", 40), HeadSHA: receipt.Record.HeadSHA,
		Gates: []GateEvidence{{Name: "remote-build", Status: GatePassed, RemoteCI: &receipt}}}
	if err := bundle.ValidateHead(bundle.HeadSHA); err == nil || !strings.Contains(err.Error(), "trusted remote") {
		t.Fatalf("ordinary ValidateHead must reject remote evidence without trust: %v", err)
	}
	if err := bundle.ValidateHeadWithRemoteCI(bundle.HeadSHA, map[string]RemoteCIPolicy{"remote-build": policy}); err != nil {
		t.Fatalf("trusted exact-head evidence should pass: %v", err)
	}
	if err := bundle.ValidateHeadWithRemoteCI(strings.Repeat("c", 40), map[string]RemoteCIPolicy{"remote-build": policy}); err == nil {
		t.Fatal("stale head was accepted")
	}
}

func TestRemoteCIProofRejectsForgeryAndWrongPolicy(t *testing.T) {
	receipt, policy := testRemoteCI(t)
	original := receipt
	wrong := policy
	wrong.DefinitionSHA256 = strings.Repeat("f", 64)
	cases := []struct {
		name   string
		mutate func(*RemoteCIReceipt, *RemoteCIPolicy)
	}{
		{"wrong pipeline definition", func(_ *RemoteCIReceipt, p *RemoteCIPolicy) { *p = wrong }},
		{"untrusted signing key", func(_ *RemoteCIReceipt, p *RemoteCIPolicy) { p.SignerKeyID = "other" }},
		{"modified job", func(r *RemoteCIReceipt, _ *RemoteCIPolicy) { r.Record.JobID = "different" }},
		{"modified worker", func(r *RemoteCIReceipt, _ *RemoteCIPolicy) { r.Record.WorkerID = "other" }},
		{"modified artifact", func(r *RemoteCIReceipt, _ *RemoteCIPolicy) { r.Record.Artifacts[0].SHA256 = strings.Repeat("f", 64) }},
		{"forged exit", func(r *RemoteCIReceipt, _ *RemoteCIPolicy) { r.Record.ExitCode = codePtr(2) }},
		{"malformed signature", func(r *RemoteCIReceipt, _ *RemoteCIPolicy) { r.Signature = "not-base64!" }},
		{"unsigned", func(r *RemoteCIReceipt, _ *RemoteCIPolicy) { r.Signature = "" }},
		{"different provider", func(_ *RemoteCIReceipt, p *RemoteCIPolicy) { p.Provider = "buildkite" }},
		{"different repository", func(_ *RemoteCIReceipt, p *RemoteCIPolicy) { p.Repository = "dporkka/other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := original
			r.Record.Artifacts = append([]CIArtifact(nil), original.Record.Artifacts...)
			p := policy
			tc.mutate(&r, &p)
			if err := r.Verify(original.Record.HeadSHA, p); err == nil {
				t.Fatal("accepted forged/untrusted CI evidence")
			}
		})
	}
}

func TestRemoteCIProofRejectsSignedButIneligibleExecution(t *testing.T) {
	original, _ := testRemoteCI(t)
	// A new signature from another key must not be enough, even for syntactically valid data.
	for _, tc := range []struct {
		name   string
		mutate func(*RemoteCIRecord)
	}{
		{"missing assignment", func(r *RemoteCIRecord) { r.WorkerID = "" }},
		{"no start", func(r *RemoteCIRecord) { r.StartedAt = time.Time{} }},
		{"bad timestamps", func(r *RemoteCIRecord) { r.AssignedAt = r.QueuedAt.Add(-time.Second) }},
		{"missing artifact digest", func(r *RemoteCIRecord) { r.Artifacts[0].SHA256 = "" }},
		{"not terminal", func(r *RemoteCIRecord) {
			r.State = CIStateRunning
			r.Outcome = ""
			r.FinishedAt = time.Time{}
			r.ExitCode = nil
		}},
		{"infra failure", func(r *RemoteCIRecord) { r.Outcome = CIOutcomeInfrastructureFailure; r.ExitCode = nil }},
		{"test failed", func(r *RemoteCIRecord) { r.Outcome = CIOutcomeTestsFailed; r.ExitCode = codePtr(1) }},
		{"nonzero claimed pass", func(r *RemoteCIRecord) { r.ExitCode = codePtr(1) }},
		{"invalid head", func(r *RemoteCIRecord) { r.HeadSHA = "short" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := original.Record
			r.Artifacts = append([]CIArtifact(nil), r.Artifacts...)
			tc.mutate(&r)
			// Sign with original signer is not needed to test semantic validation:
			// invalid records must fail before cryptographic acceptance.
			if err := r.ValidateReleaseSuccess(r.HeadSHA); err == nil {
				t.Fatalf("ineligible execution accepted: %+v", r)
			}
		})
	}

}

func TestRemoteCILifecycleDifferentiatesQueuedFromTerminalSuccess(t *testing.T) {
	receipt, _ := testRemoteCI(t)
	r := receipt.Record
	r.State = CIStateQueued
	r.WorkerID = ""
	r.AssignedAt = time.Time{}
	r.StartedAt = time.Time{}
	r.FinishedAt = time.Time{}
	r.Outcome = ""
	r.ExitCode = nil
	r.Artifacts = nil
	if err := r.Validate(); err != nil {
		t.Fatalf("queued observation invalid: %v", err)
	}
	if err := r.ValidateReleaseSuccess(r.HeadSHA); err == nil {
		t.Fatal("queued job accepted as success")
	}
	r.State = CIStateAssigned
	r.WorkerID = "worker-22"
	r.AssignedAt = r.QueuedAt.Add(time.Second)
	if err := r.Validate(); err != nil {
		t.Fatalf("assigned observation invalid: %v", err)
	}
	r.State = CIStateRunning
	r.StartedAt = r.AssignedAt.Add(time.Second)
	if err := r.Validate(); err != nil {
		t.Fatalf("running observation invalid: %v", err)
	}
}

func TestRemoteCIReceiptCannotImpersonateHumanApproval(t *testing.T) {
	receipt, policy := testRemoteCI(t)
	for _, name := range []string{GateIndependentReview, GateHumanApproval} {
		bundle := EvidenceBundle{BaseSHA: strings.Repeat("b", 40), HeadSHA: receipt.Record.HeadSHA,
			Gates: []GateEvidence{{Name: name, Status: GatePassed, RemoteCI: &receipt}}}
		if err := bundle.ValidateHeadWithRemoteCI(bundle.HeadSHA, map[string]RemoteCIPolicy{name: policy}); err == nil {
			t.Fatalf("remote CI proof impersonated %s", name)
		}
	}
}

func TestRemoteCIReceiptRejectsDuplicateEvidenceGates(t *testing.T) {
	receipt, policy := testRemoteCI(t)
	bundle := EvidenceBundle{BaseSHA: strings.Repeat("b", 40), HeadSHA: receipt.Record.HeadSHA,
		Gates: []GateEvidence{
			{Name: "remote-build", Status: GatePassed, RemoteCI: &receipt},
			{Name: "remote-build", Status: GatePassed, RemoteCI: &receipt},
		}}
	if err := bundle.ValidateHeadWithRemoteCI(bundle.HeadSHA, map[string]RemoteCIPolicy{"remote-build": policy}); err == nil {
		t.Fatal("duplicate CI gates were accepted")
	}
}

func TestRemoteCIProofRejectsLocalAndRemoteEvidenceOnSameGate(t *testing.T) {
	receipt, policy := testRemoteCI(t)
	bundle := EvidenceBundle{BaseSHA: strings.Repeat("b", 40), HeadSHA: receipt.Record.HeadSHA,
		Gates: []GateEvidence{{Name: "remote-build", Status: GatePassed, Command: "true", RemoteCI: &receipt}}}
	if err := bundle.ValidateHeadWithRemoteCI(bundle.HeadSHA, map[string]RemoteCIPolicy{"remote-build": policy}); err == nil {
		t.Fatal("ambiguous local and remote execution accepted")
	}
}

func TestNewRemoteCICancelOutcomeUsesUSWireSpelling(t *testing.T) {
	if CIOutcomeCanceled != CIOutcome("canceled") {
		t.Fatalf("new remote CI cancellation outcome changed: %q", CIOutcomeCanceled)
	}
}
