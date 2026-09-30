package gateway

import (
	"context"
	"testing"

	"github.com/ai-dev-control-plane/vcs"
)

func TestGiteaBranchPublisherUsesUsernameAndToken(t *testing.T) {
	inner := &captureBranchPublisher{t: t}
	publisher := NewGiteaBranchPublisher(inner, "alice", "secret-token")

	err := publisher.Publish(context.Background(), vcs.PublishRequest{
		WorkspacePath: t.TempDir(),
		Ref:           "feature/test",
		Remote:        "origin",
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := inner.request.Env["DEV_PLANE_GIT_USERNAME"]; got != "alice" {
		t.Fatalf("username = %q, want alice", got)
	}
	if got := inner.request.Env["DEV_PLANE_GIT_PASSWORD"]; got != "secret-token" {
		t.Fatalf("password = %q, want secret-token", got)
	}
}
