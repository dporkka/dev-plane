package prfactory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/gateway"
)

func TestCreateForgePRRequiresForgeAndCredential(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	factory := NewFactory(nil, nil)

	if _, err := factory.createForgePR(context.Background(), "owner", "repo", "title", "body", "head", "main", false); err == nil {
		t.Fatal("expected missing forge error")
	} else if !strings.Contains(err.Error(), "forge") {
		t.Fatalf("error = %v, want forge", err)
	}

	factory.forge = &recordingForge{}
	if _, err := factory.createForgePR(context.Background(), "owner", "repo", "title", "body", "head", "main", false); err == nil {
		t.Fatal("expected missing credential error")
	} else if !strings.Contains(err.Error(), "credential") {
		t.Fatalf("error = %v, want credential", err)
	}
}

func TestCreateForgePRPropagatesProviderError(t *testing.T) {
	wantErr := errors.New("provider rejected request")
	forge := &errorForge{err: wantErr}
	factory := NewFactory(nil, nil).WithForge(forge, gateway.ForgeCredential{AccessToken: "token"})

	_, err := factory.createForgePR(context.Background(), "owner", "repo", "title", "body", "agent/task", "main", false)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

type errorForge struct {
	err error
}

func (f *errorForge) Name() string { return "error" }
func (f *errorForge) ListRepositories(context.Context, gateway.ForgeCredential, int) ([]gateway.ForgeRepository, error) {
	return nil, f.err
}
func (f *errorForge) GetRepository(context.Context, gateway.ForgeCredential, string, string) (*gateway.ForgeRepository, error) {
	return nil, f.err
}
func (f *errorForge) CreatePullRequest(context.Context, gateway.ForgeCredential, string, string, gateway.ForgeNewPullRequest) (*gateway.ForgePullRequest, error) {
	return nil, f.err
}
func (f *errorForge) MergePullRequest(context.Context, gateway.ForgeCredential, string, string, int, gateway.ForgeMergeRequest) (*gateway.ForgeMergeResult, error) {
	return nil, f.err
}
func (f *errorForge) CreateWebhook(context.Context, gateway.ForgeCredential, string, string, string, string) (int64, error) {
	return 0, f.err
}
func (f *errorForge) DeleteWebhook(context.Context, gateway.ForgeCredential, string, string, int64) error {
	return f.err
}
