// Package contracttest provides reusable behavioral conformance tests for
// third-party Dev Plane runtime providers.
package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/runtimes"
)

// ProviderFactory returns a fresh provider instance for one contract run.
type ProviderFactory func(t *testing.T) runtimes.Provider

// FixtureFactory returns a repository-backed workspace request suitable for the
// provider under test. The repository must contain seed.txt with "before\n".
type FixtureFactory func(t *testing.T) runtimes.CreateRequest

// Run executes the stable behavioral contract every runtime Provider is
// expected to satisfy. Provider authors can invoke this from their own tests.
func Run(t *testing.T, newProvider ProviderFactory, newFixture FixtureFactory) {
	t.Helper()

	provider := newProvider(t)
	req := newFixture(t)
	ctx := context.Background()

	session, err := provider.CreateWorkspace(ctx, req)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if session == nil || session.ID == "" {
		t.Fatal("CreateWorkspace returned an empty session")
	}
	if session.Status != "ready" {
		t.Fatalf("session status = %q, want ready", session.Status)
	}
	if session.Provider == "" {
		t.Fatal("session provider must identify its implementation")
	}

	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			_ = provider.DestroyWorkspace(context.Background(), session.ID)
		}
	})

	status, err := provider.GetStatus(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.SessionID != session.ID {
		t.Fatalf("status session id = %q, want %q", status.SessionID, session.ID)
	}

	if err := provider.WriteFile(ctx, session.ID, "nested/value.txt", []byte("hello\n")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := provider.ReadFile(ctx, session.ID, "nested/value.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("ReadFile = %q, want hello", data)
	}

	result, err := provider.ExecuteCommand(ctx, session.ID, runtimes.Command{
		Args:    []string{"git", "status", "--short"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExecuteCommand exit = %d, stderr=%q", result.ExitCode, result.Stderr)
	}

	patch := "diff --git a/seed.txt b/seed.txt\n--- a/seed.txt\n+++ b/seed.txt\n@@ -1 +1 @@\n-before\n+patched\n"
	if err := provider.ApplyPatch(ctx, session.ID, patch); err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	data, err = provider.ReadFile(ctx, session.ID, "seed.txt")
	if err != nil {
		t.Fatalf("ReadFile after patch: %v", err)
	}
	if string(data) != "patched\n" {
		t.Fatalf("patched seed = %q, want patched", data)
	}

	snapshot, err := provider.Snapshot(ctx, session.ID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot == nil || snapshot.GitCommit == "" {
		t.Fatal("Snapshot must return a restorable git revision")
	}

	if err := provider.WriteFile(ctx, session.ID, "seed.txt", []byte("after\n")); err != nil {
		t.Fatalf("WriteFile after snapshot: %v", err)
	}
	if err := provider.Restore(ctx, session.ID, snapshot); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	data, err = provider.ReadFile(ctx, session.ID, "seed.txt")
	if err != nil {
		t.Fatalf("ReadFile after restore: %v", err)
	}
	if string(data) != "patched\n" {
		t.Fatalf("restored seed = %q, want patched", data)
	}

	logCtx, cancel := context.WithCancel(ctx)
	logs, err := provider.StreamLogs(logCtx, session.ID)
	if err != nil {
		cancel()
		t.Fatalf("StreamLogs: %v", err)
	}
	if logs == nil {
		cancel()
		t.Fatal("StreamLogs returned a nil channel")
	}
	cancel()

	if err := provider.DestroyWorkspace(ctx, session.ID); err != nil {
		t.Fatalf("DestroyWorkspace: %v", err)
	}
	destroyed = true

	if _, err := provider.GetStatus(ctx, session.ID); !errors.Is(err, runtimes.ErrSessionNotFound) {
		t.Fatalf("GetStatus after destroy error = %v, want ErrSessionNotFound", err)
	}
}
