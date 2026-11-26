package prfactory

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	rp "github.com/ai-dev-control-plane/repoprotocol"
)

func TestGiteaCIRequiresTrustedExactAttempt(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	head, base := strings.Repeat("a", 40), strings.Repeat("b", 40)
	repo := "gitea:https://forge.example#77"
	now := time.Now().UTC()
	zero := 0
	record := rp.RemoteCIRecord{Version: 1, Provider: "fixture", Repository: repo, HeadSHA: head, PipelineID: "1", DefinitionSHA256: strings.Repeat("c", 64), RunID: "2", JobID: "3", AttemptID: "4", WorkerID: "worker", State: rp.CIStateTerminal, Outcome: rp.CIOutcomePassed, ExitCode: &zero, QueuedAt: now.Add(-time.Minute), AssignedAt: now.Add(-50 * time.Second), StartedAt: now.Add(-40 * time.Second), FinishedAt: now.Add(-time.Second)}
	receipt, err := rp.SignRemoteCIReceipt(record, "key", priv)
	if err != nil {
		t.Fatal(err)
	}
	policy := rp.RemoteCIPolicy{Provider: record.Provider, Repository: repo, DefinitionSHA256: record.DefinitionSHA256, SignerKeyID: "key", PublicKey: pub, PipelineID: "1", RunID: "2", JobID: "3", AttemptID: "4"}
	bundle := rp.EvidenceBundle{WorkItemID: "item", BaseSHA: base, HeadSHA: head, Gates: []rp.GateEvidence{{Name: "build", Status: rp.GatePassed, RemoteCI: &receipt}}}
	pins := map[string]rp.RemoteCIPolicy{"build": policy}
	if err := validateGiteaCI(bundle, "item", repo, base, head, pins); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*rp.EvidenceBundle, map[string]rp.RemoteCIPolicy)
	}{
		{"wrong base", func(b *rp.EvidenceBundle, _ map[string]rp.RemoteCIPolicy) { b.BaseSHA = head }},
		{"wrong work item", func(b *rp.EvidenceBundle, _ map[string]rp.RemoteCIPolicy) { b.WorkItemID = "other" }},
		{"missing gate", func(b *rp.EvidenceBundle, _ map[string]rp.RemoteCIPolicy) { b.Gates = nil }},
		{"unsigned", func(b *rp.EvidenceBundle, _ map[string]rp.RemoteCIPolicy) { b.Gates[0].RemoteCI = nil }},
		{"old attempt", func(_ *rp.EvidenceBundle, p map[string]rp.RemoteCIPolicy) {
			v := p["build"]
			v.AttemptID = "5"
			p["build"] = v
		}},
		{"wrong instance", func(_ *rp.EvidenceBundle, p map[string]rp.RemoteCIPolicy) {
			v := p["build"]
			v.Repository = "gitea:https://other.example#77"
			p["build"] = v
		}},
		{"wrong gate", func(b *rp.EvidenceBundle, _ map[string]rp.RemoteCIPolicy) { b.Gates[0].Name = "other" }},
		{"empty trusted policy", func(_ *rp.EvidenceBundle, p map[string]rp.RemoteCIPolicy) { delete(p, "build") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bundle
			b.Gates = append([]rp.GateEvidence(nil), bundle.Gates...)
			p := map[string]rp.RemoteCIPolicy{"build": policy}
			tc.change(&b, p)
			if validateGiteaCI(b, "item", repo, base, head, p) == nil {
				t.Fatal("untrusted evidence accepted")
			}
		})
	}
}
