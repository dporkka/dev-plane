package agentrunner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/tools"
	"github.com/ai-dev-control-plane/execution"
)

func TestExecuteToolUncheckedRejectsStaleWorkerBeforeWritingFile(t *testing.T) {
	root := t.TempDir()
	runner := &Runner{tools: tools.NewWorkspaceTools(slog.Default())}
	ctx := execution.WithEffectGuard(context.Background(), func(context.Context) error { return execution.ErrStaleEpoch })
	_, err := runner.executeToolUnchecked(ctx, "write_file", root, json.RawMessage(`{"path":"should-not-exist.txt","content":"unsafe"}`))
	if !errors.Is(err, execution.ErrStaleEpoch) {
		t.Fatalf("stale worker write not blocked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "should-not-exist.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale worker wrote file: %v", err)
	}
}

func TestExecuteRuntimeToolRejectsStaleWorkerBeforeCallingProvider(t *testing.T) {
	provider := &fakeRuntimeProvider{files: map[string][]byte{}}
	runner := &Runner{}
	ctx := execution.WithEffectGuard(context.Background(), func(context.Context) error { return execution.ErrStaleEpoch })
	_, err := runner.executeRuntimeTool(ctx, provider, "session-1", "write_file", json.RawMessage(`{"path":"unsafe.txt","content":"not authorized"}`))
	if !errors.Is(err, execution.ErrStaleEpoch) {
		t.Fatalf("stale runtime write not blocked: %v", err)
	}
	if _, exists := provider.files["unsafe.txt"]; exists {
		t.Fatal("stale worker wrote to runtime")
	}
}
