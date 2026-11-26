package handlers

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/prfactory"
)

func TestWorkerApprovalRejectsInvalidForgeConfiguration(t *testing.T) {
	t.Setenv("FORGE_PROVIDER", "gitea")
	t.Setenv("GITEA_URL", "")
	t.Setenv("GITEA_TOKEN", "synthetic-token")
	t.Setenv("GITHUB_TOKEN", "must-not-fallback")
	db := setupApprovalHandlerDB(t)
	defer func() { _ = db.Close() }()
	insertApprovalTaskFixture(t, db, "task-1", "reviewing")
	h := NewApprovalHandler(db, slog.Default(), nil)
	err := h.HandleApprovalApproved(&nats.Msg{Data: []byte(`{"approval_id":"approval-1","task_id":"task-1","response":"approved","approval_type":"pr_create"}`)})
	if !errors.Is(err, prfactory.ErrForgeConfiguration) {
		t.Fatalf("expected runtime config refusal, got %v", err)
	}
}
