package changeset

import (
	"reflect"
	"testing"

	"github.com/ai-dev-control-plane/changegraph"
)

func TestEvaluateReadyWithInternalChainAndMergedExternalFrontier(t *testing.T) {
	input := Input{
		Members: []Member{
			{ID: "api", CommitSHA: "sha-api", TreeHash: "tree-api", DecisionDigest: "decision-api", Approvable: true},
			{ID: "web", CommitSHA: "sha-web", TreeHash: "tree-web", DecisionDigest: "decision-web", Approvable: true},
		},
		Graph: changegraph.Graph{Nodes: []changegraph.Node{
			{ID: "schema"},
			{ID: "api", DependsOn: []string{"schema"}},
			{ID: "web", DependsOn: []string{"api"}},
		}},
		States: map[string]changegraph.State{
			"schema": changegraph.StateMerged,
			"api":    changegraph.StateOpen,
			"web":    changegraph.StateOpen,
		},
	}

	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate() error: %v", err)
	}
	if !result.Ready {
		t.Fatalf("Evaluate().Ready = false, blockers = %#v", result.Blockers)
	}
	if !reflect.DeepEqual(result.PublicationOrder, []string{"api", "web"}) {
		t.Fatalf("publication order = %#v, want [api web]", result.PublicationOrder)
	}
	if len(result.Blockers) != 0 {
		t.Fatalf("blockers = %#v, want empty", result.Blockers)
	}
}

func TestEvaluateBlocksUnmergedExternalFrontier(t *testing.T) {
	input := Input{
		Members: []Member{
			{ID: "api", CommitSHA: "sha-api", TreeHash: "tree-api", DecisionDigest: "decision-api", Approvable: true},
			{ID: "web", CommitSHA: "sha-web", TreeHash: "tree-web", DecisionDigest: "decision-web", Approvable: true},
		},
		Graph: changegraph.Graph{Nodes: []changegraph.Node{
			{ID: "schema"},
			{ID: "api", DependsOn: []string{"schema"}},
			{ID: "web", DependsOn: []string{"api"}},
		}},
		States: map[string]changegraph.State{
			"schema": changegraph.StateOpen,
			"api":    changegraph.StateOpen,
			"web":    changegraph.StateOpen,
		},
	}

	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate() error: %v", err)
	}
	if result.Ready {
		t.Fatal("Evaluate().Ready = true, want false")
	}
	want := []Blocker{{CandidateID: "schema", Reason: "external-dependency-not-merged"}}
	if !reflect.DeepEqual(result.Blockers, want) {
		t.Fatalf("blockers = %#v, want %#v", result.Blockers, want)
	}
}

func TestEvaluateBlocksMemberWithoutAuthority(t *testing.T) {
	input := Input{
		Members: []Member{
			{ID: "api", CommitSHA: "sha-api", TreeHash: "tree-api", DecisionDigest: "", Approvable: true},
			{ID: "web", CommitSHA: "sha-web", TreeHash: "tree-web", DecisionDigest: "decision-web", Approvable: false},
		},
		Graph: changegraph.Graph{Nodes: []changegraph.Node{
			{ID: "api"},
			{ID: "web", DependsOn: []string{"api"}},
		}},
		States: map[string]changegraph.State{
			"api": changegraph.StateOpen,
			"web": changegraph.StateOpen,
		},
	}

	result, err := Evaluate(input)
	if err != nil {
		t.Fatalf("Evaluate() error: %v", err)
	}
	if result.Ready {
		t.Fatal("Evaluate().Ready = true, want false")
	}
	want := []Blocker{
		{CandidateID: "api", Reason: "missing-authority"},
		{CandidateID: "web", Reason: "review-not-approvable"},
	}
	if !reflect.DeepEqual(result.Blockers, want) {
		t.Fatalf("blockers = %#v, want %#v", result.Blockers, want)
	}
}

func TestManifestDigestIsDeterministicAcrossInputOrder(t *testing.T) {
	left := ManifestInput{
		ChangeSetID: "set-1",
		ProjectID:   "project-1",
		Members: []Member{
			{ID: "web", CommitSHA: "sha-web", TreeHash: "tree-web", DecisionDigest: "decision-web", Approvable: true},
			{ID: "api", CommitSHA: "sha-api", TreeHash: "tree-api", DecisionDigest: "decision-api", Approvable: true},
		},
		Graph: changegraph.Graph{Nodes: []changegraph.Node{
			{ID: "web", DependsOn: []string{"api"}},
			{ID: "api"},
		}},
	}
	right := ManifestInput{
		ChangeSetID: "set-1",
		ProjectID:   "project-1",
		Members: []Member{
			{ID: "api", CommitSHA: "sha-api", TreeHash: "tree-api", DecisionDigest: "decision-api", Approvable: true},
			{ID: "web", CommitSHA: "sha-web", TreeHash: "tree-web", DecisionDigest: "decision-web", Approvable: true},
		},
		Graph: changegraph.Graph{Nodes: []changegraph.Node{
			{ID: "api"},
			{ID: "web", DependsOn: []string{"api"}},
		}},
	}

	leftManifest, err := NewManifest(left)
	if err != nil {
		t.Fatalf("NewManifest(left) error: %v", err)
	}
	rightManifest, err := NewManifest(right)
	if err != nil {
		t.Fatalf("NewManifest(right) error: %v", err)
	}
	leftDigest, err := leftManifest.Digest()
	if err != nil {
		t.Fatalf("left Digest() error: %v", err)
	}
	rightDigest, err := rightManifest.Digest()
	if err != nil {
		t.Fatalf("right Digest() error: %v", err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("digests differ: %s != %s", leftDigest, rightDigest)
	}
}

func TestManifestDigestChangesWhenCandidateIdentityOrTopologyChanges(t *testing.T) {
	baseInput := ManifestInput{
		ChangeSetID: "set-1",
		ProjectID:   "project-1",
		Members: []Member{
			{ID: "api", CommitSHA: "sha-api", TreeHash: "tree-api", DecisionDigest: "decision-api", Approvable: true},
			{ID: "web", CommitSHA: "sha-web", TreeHash: "tree-web", DecisionDigest: "decision-web", Approvable: true},
		},
		Graph: changegraph.Graph{Nodes: []changegraph.Node{
			{ID: "api"},
			{ID: "web", DependsOn: []string{"api"}},
		}},
	}
	base, err := NewManifest(baseInput)
	if err != nil {
		t.Fatalf("NewManifest(base) error: %v", err)
	}
	baseDigest, _ := base.Digest()

	identityInput := baseInput
	identityInput.Members = append([]Member(nil), baseInput.Members...)
	identityInput.Members[0].CommitSHA = "sha-api-v2"
	identity, err := NewManifest(identityInput)
	if err != nil {
		t.Fatalf("NewManifest(identity) error: %v", err)
	}
	identityDigest, _ := identity.Digest()
	if identityDigest == baseDigest {
		t.Fatal("candidate identity change did not change digest")
	}

	topologyInput := baseInput
	topologyInput.Graph = changegraph.Graph{Nodes: []changegraph.Node{
		{ID: "api", DependsOn: []string{"web"}},
		{ID: "web"},
	}}
	topology, err := NewManifest(topologyInput)
	if err != nil {
		t.Fatalf("NewManifest(topology) error: %v", err)
	}
	topologyDigest, _ := topology.Digest()
	if topologyDigest == baseDigest {
		t.Fatal("topology change did not change digest")
	}
}
