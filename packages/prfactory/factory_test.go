package prfactory

import (
	"context"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/forge"
)

func TestOpenForgeChangeRequiresProvider(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	factory := NewFactory(nil, nil)

	if _, err := factory.openForgeChange(context.Background(), "owner", "repo", "title", "body", "head", "main", false); err == nil {
		t.Fatal("expected missing forge provider error")
	} else if !strings.Contains(err.Error(), "forge provider") {
		t.Fatalf("error = %v, want forge provider", err)
	}
}

func TestOpenForgeChangeAllowsProviderManagedAuthentication(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	provider := &fakeForgeProvider{}
	factory := NewFactory(nil, nil).WithForgeProvider(provider)

	if _, err := factory.openForgeChange(context.Background(), "owner", "repo", "title", "body", "head", "main", false); err != nil {
		t.Fatalf("openForgeChange() error = %v, want provider-managed authentication", err)
	}
	if provider.credential.Token != "" {
		t.Fatalf("credential token = %q, want empty", provider.credential.Token)
	}
}

func TestOpenForgeChangeSendsNeutralCredentialAndDraftPayload(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	provider := &fakeForgeProvider{openResult: &forge.Change{
		Number: 42,
		URL:    "https://forge.example/owner/repo/changes/42",
		State:  forge.ChangeStateOpen,
	}}
	factory := NewFactory(nil, nil).
		WithForgeProvider(provider).
		WithForgeCredential("forge-token")

	change, err := factory.openForgeChange(context.Background(), "owner", "repo", "title", "body", "agent/task", "main", true)
	if err != nil {
		t.Fatalf("openForgeChange() error: %v", err)
	}
	if change.Number != 42 {
		t.Fatalf("change number = %d, want 42", change.Number)
	}
	if provider.credential.Token != "forge-token" {
		t.Fatalf("token = %q, want forge-token", provider.credential.Token)
	}
	if provider.repository != (forge.Repository{Namespace: "owner", Name: "repo"}) {
		t.Fatalf("repo = %+v, want owner/repo", provider.repository)
	}
	if provider.openRequest.Title != "title" || provider.openRequest.Head != "agent/task" || provider.openRequest.Base != "main" || !provider.openRequest.Draft {
		t.Fatalf("request = %+v", provider.openRequest)
	}
}

type fakeForgeProvider struct {
	credential   forge.Credential
	repository   forge.Repository
	openRequest  forge.OpenChangeRequest
	mergeRequest forge.MergeChangeRequest
	openResult   *forge.Change
	mergeResult  *forge.MergeResult
	err          error
}

func (f *fakeForgeProvider) Name() string { return "fake" }

func (f *fakeForgeProvider) OpenChange(_ context.Context, credential forge.Credential, repository forge.Repository, req forge.OpenChangeRequest) (*forge.Change, error) {
	f.credential = credential
	f.repository = repository
	f.openRequest = req
	if f.err != nil {
		return nil, f.err
	}
	if f.openResult != nil {
		return f.openResult, nil
	}
	return &forge.Change{
		Number: 1,
		URL:    "https://forge.example/owner/repo/changes/1",
		State:  forge.ChangeStateOpen,
	}, nil
}

func (f *fakeForgeProvider) MergeChange(_ context.Context, credential forge.Credential, repository forge.Repository, _ int, req forge.MergeChangeRequest) (*forge.MergeResult, error) {
	f.credential = credential
	f.repository = repository
	f.mergeRequest = req
	if f.err != nil {
		return nil, f.err
	}
	if f.mergeResult != nil {
		return f.mergeResult, nil
	}
	return &forge.MergeResult{Merged: true, Revision: "merge-1"}, nil
}

var _ forge.Provider = (*fakeForgeProvider)(nil)
