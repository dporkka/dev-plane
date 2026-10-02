package projectbrain

import (
	"context"
	"strings"
	"testing"
	"time"
)

type fakeGitNexusClient struct {
	snapshot    GitNexusSnapshot
	result      GitNexusResult
	snapshotErr error
	queryErr    error
	gotRepo     string
	gotTerms    []string
	gotLimit    int
}

func (f *fakeGitNexusClient) Snapshot(_ context.Context, repository string) (GitNexusSnapshot, error) {
	f.gotRepo = repository
	return f.snapshot, f.snapshotErr
}

func (f *fakeGitNexusClient) Query(_ context.Context, repository string, terms []string, limit int) (GitNexusResult, error) {
	f.gotRepo = repository
	f.gotTerms = append([]string(nil), terms...)
	f.gotLimit = limit
	return f.result, f.queryErr
}

func TestGitNexusSourceRejectsStaleIndex(t *testing.T) {
	client := &fakeGitNexusClient{snapshot: GitNexusSnapshot{LastCommit: "def456"}}
	source := NewGitNexusSource(client, GitNexusSourceOptions{})
	_, err := source.Facts(context.Background(), Query{Repository: "repo-1", Revision: "git-commit:abc123"})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("Facts() error = %v, want stale index error", err)
	}
}

func TestGitNexusSourceRequiresCommitRevision(t *testing.T) {
	client := &fakeGitNexusClient{}
	source := NewGitNexusSource(client, GitNexusSourceOptions{})
	_, err := source.Facts(context.Background(), Query{Repository: "repo-1", Revision: "git-tree:abc123"})
	if err == nil || !strings.Contains(err.Error(), "git-commit") {
		t.Fatalf("Facts() error = %v, want git-commit revision error", err)
	}
}

func TestGitNexusSourceConvertsGraphObservations(t *testing.T) {
	observed := time.Date(2026, 10, 2, 18, 0, 0, 0, time.UTC)
	client := &fakeGitNexusClient{
		snapshot: GitNexusSnapshot{LastCommit: "abc123", IndexedAt: observed},
		result: GitNexusResult{
			Nodes:     []GitNexusNode{{UID: "fn:saveInvoice", Kind: "Function", Name: "saveInvoice", FilePath: "apps/api/invoice.go", StartLine: 42, Confidence: 0.98}},
			Relations: []GitNexusRelation{{From: "saveInvoice", Type: "CALLS", To: "persistInvoice", Confidence: 0.91}},
			Processes: []GitNexusProcess{{Name: "invoice save", Steps: []GitNexusProcessStep{{Position: 1, Symbol: "saveInvoice", FilePath: "apps/api/invoice.go", StartLine: 42}}}},
		},
	}
	source := NewGitNexusSource(client, GitNexusSourceOptions{MaxResults: 37})
	facts, err := source.Facts(context.Background(), Query{
		Repository: "repo-1",
		Revision:   "git-commit:abc123",
		Terms:      []string{"invoice", "save"},
	})
	if err != nil {
		t.Fatalf("Facts() error = %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("len(facts) = %d, want 3: %#v", len(facts), facts)
	}
	if client.gotRepo != "repo-1" || client.gotLimit != 37 || strings.Join(client.gotTerms, ",") != "invoice,save" {
		t.Fatalf("client query = repo %q terms %#v limit %d", client.gotRepo, client.gotTerms, client.gotLimit)
	}
	for _, fact := range facts {
		if fact.Repository != "repo-1" || fact.Revision != "git-commit:abc123" || fact.Scope != ScopeRevision {
			t.Fatalf("fact revision binding = %#v", fact)
		}
		if len(fact.Provenance) != 1 || fact.Provenance[0].Source != "gitnexus" || !fact.Provenance[0].ObservedAt.Equal(observed) {
			t.Fatalf("fact provenance = %#v", fact.Provenance)
		}
	}
	if facts[0].Subject != "symbol:saveInvoice" || facts[0].Relation != "defined_in" || facts[0].Object != "apps/api/invoice.go:42" {
		t.Fatalf("node fact = %#v", facts[0])
	}
	if facts[1].Relation != "calls" || facts[1].Object != "symbol:persistInvoice" {
		t.Fatalf("relation fact = %#v", facts[1])
	}
	if facts[2].Subject != "process:invoice save" || facts[2].Relation != "step" {
		t.Fatalf("process fact = %#v", facts[2])
	}
}
