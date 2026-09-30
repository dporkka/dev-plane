package gateway

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/vcs"
)

type captureBranchPublisher struct {
	t       *testing.T
	request vcs.PublishRequest
}

func (p *captureBranchPublisher) Publish(_ context.Context, req vcs.PublishRequest) error {
	p.request = req

	if got := req.Env["DEV_PLANE_GIT_USERNAME"]; got != "x-access-token" {
		p.t.Fatalf("username = %q, want x-access-token", got)
	}
	if got := req.Env["DEV_PLANE_GIT_PASSWORD"]; got != "secret-token" {
		p.t.Fatalf("password = %q, want secret-token", got)
	}
	askpass := req.Env["GIT_ASKPASS"]
	if askpass == "" {
		p.t.Fatal("GIT_ASKPASS is missing")
	}
	data, err := os.ReadFile(askpass)
	if err != nil {
		p.t.Fatalf("read askpass: %v", err)
	}
	if strings.Contains(string(data), "secret-token") {
		p.t.Fatal("GitHub token was embedded into askpass script")
	}
	return nil
}

func TestGitHubBranchPublisherOwnsGitHubHTTPSCredentialMapping(t *testing.T) {
	inner := &captureBranchPublisher{t: t}
	publisher := NewGitHubBranchPublisher(inner, "secret-token")

	err := publisher.Publish(context.Background(), vcs.PublishRequest{
		WorkspacePath: t.TempDir(),
		Ref:           "feature/test",
		Remote:        "origin",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := interface{}(publisher).(vcs.Publisher); !ok {
		t.Fatal("GitHub branch publisher does not implement vcs.Publisher")
	}
}
