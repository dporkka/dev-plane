package agentrunner

import (
	"encoding/json"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/execution"
	"github.com/ai-dev-control-plane/policies"
)

func TestCapabilityApprovalMetadataBindsExactAuthorityGrant(t *testing.T) {
	grant := execution.Grant{
		Operation: capability.OpMergePR,
		Resource:  "repo:dporkka/dev-plane/pr:123",
		Revision:  "abc123",
	}
	decision := &capabilityDecisionError{
		toolName:  "merge_pr",
		operation: capability.OpMergePR,
		resource:  grant.Resource,
		result: &capability.Result{
			Effect:         policies.EffectAsk,
			RiskLevel:      capability.RiskLevelHigh,
			Reason:         "requires approval",
			RequestedGrant: &grant,
		},
	}

	raw, err := capabilityApprovalMetadata(decision)
	if err != nil {
		t.Fatalf("capabilityApprovalMetadata() error = %v", err)
	}

	var metadata struct {
		AuthorityGrant struct {
			Operation string `json:"operation"`
			Resource  string `json:"resource"`
			Revision  string `json:"revision"`
		} `json:"authority_grant"`
		AuthorityDigest string `json:"authority_digest"`
	}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	if metadata.AuthorityGrant.Operation != grant.Operation ||
		metadata.AuthorityGrant.Resource != grant.Resource ||
		metadata.AuthorityGrant.Revision != grant.Revision {
		t.Fatalf("authority grant = %#v, want %#v", metadata.AuthorityGrant, grant)
	}

	manifest, err := execution.NewManifest(grant)
	if err != nil {
		t.Fatalf("NewManifest() error = %v", err)
	}
	wantDigest, err := manifest.Digest()
	if err != nil {
		t.Fatalf("Manifest.Digest() error = %v", err)
	}
	if metadata.AuthorityDigest != wantDigest {
		t.Fatalf("authority digest = %q, want %q", metadata.AuthorityDigest, wantDigest)
	}
}

func TestCapabilityApprovalMetadataOmitsAuthorityWhenRequestIsUnbound(t *testing.T) {
	decision := &capabilityDecisionError{
		toolName:  "run_command",
		operation: capability.OpRunCommand,
		resource:  "",
		result: &capability.Result{
			Effect:    policies.EffectAsk,
			RiskLevel: capability.RiskLevelMedium,
			Reason:    "requires approval",
		},
	}

	raw, err := capabilityApprovalMetadata(decision)
	if err != nil {
		t.Fatalf("capabilityApprovalMetadata() error = %v", err)
	}

	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(raw, &metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if _, ok := metadata["authority_grant"]; ok {
		t.Fatal("unbound approval metadata must not contain authority_grant")
	}
	if _, ok := metadata["authority_digest"]; ok {
		t.Fatal("unbound approval metadata must not contain authority_digest")
	}
}
