package runtimes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNulangCloudProviderIntegrationCheckpointRestoreFork(t *testing.T) {
	if os.Getenv("RUN_NULANG_CLOUD_INTEGRATION") != "1" {
		t.Skip("set RUN_NULANG_CLOUD_INTEGRATION=1 to run live Nulang Cloud workspace tests")
	}
	baseURL := strings.TrimSpace(os.Getenv("NULANG_CLOUD_URL"))
	if baseURL == "" {
		t.Fatal("NULANG_CLOUD_URL is required when RUN_NULANG_CLOUD_INTEGRATION=1")
	}
	token := strings.TrimSpace(os.Getenv("NULANG_CLOUD_TOKEN"))
	if token == "" {
		t.Fatal("NULANG_CLOUD_TOKEN is required when RUN_NULANG_CLOUD_INTEGRATION=1")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	repoDir := createIntegrationRepo(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	workspaceID := "devplane-kvm-" + suffix
	branch := "agent/kvm-" + suffix
	forkID := workspaceID + "-fork"

	provider := NewNulangCloudProvider(baseURL, token)
	session, err := provider.CreateWorkspace(ctx, CreateRequest{
		RepositoryID: "repo-kvm-integration",
		CloneURL: repoDir,
		BaseBranch: "main",
		Branch: branch,
		WorktreeName: workspaceID,
	})
	if err != nil {
		t.Fatalf("CreateWorkspace() error: %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_ = provider.DestroyWorkspace(cleanupCtx, session.ID)
		_ = provider.DestroyWorkspace(cleanupCtx, forkID)
	}()

	if session.Status != "ready" {
		t.Fatalf("session status = %q, want ready", session.Status)
	}

	branchResult, err := provider.ExecuteCommand(ctx, session.ID, Command{
		Args: []string{"git", "rev-parse", "--abbrev-ref", "HEAD"},
		Dir: "/workspace",
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("branch command error: %v", err)
	}
	if branchResult.ExitCode != 0 || strings.TrimSpace(branchResult.Stdout) != branch {
		t.Fatalf("branch result = exit %d stdout=%q stderr=%q, want %q", branchResult.ExitCode, branchResult.Stdout, branchResult.Stderr, branch)
	}

	readme, err := provider.ReadFile(ctx, session.ID, "README.md")
	if err != nil {
		t.Fatalf("ReadFile(README.md) error: %v", err)
	}
	if strings.TrimSpace(string(readme)) != "# Integration" {
		t.Fatalf("README.md = %q", string(readme))
	}

	if err := provider.WriteFile(ctx, session.ID, "generated.txt", []byte("inside Nulang Cloud\n")); err != nil {
		t.Fatalf("WriteFile(generated.txt) error: %v", err)
	}
	written, err := provider.ReadFile(ctx, session.ID, "generated.txt")
	if err != nil {
		t.Fatalf("ReadFile(generated.txt) error: %v", err)
	}
	if string(written) != "inside Nulang Cloud\n" {
		t.Fatalf("generated.txt = %q", string(written))
	}

	large := make([]byte, nulangCloudSingleFileBytes+1)
	for i := range large {
		large[i] = byte(i % 251)
	}
	if err := provider.WriteFile(ctx, session.ID, "large.bin", large); err != nil {
		t.Fatalf("WriteFile(large.bin) error: %v", err)
	}
	sizeResult, err := provider.ExecuteCommand(ctx, session.ID, Command{
		Args: []string{"wc", "-c", "large.bin"},
		Dir: "/workspace",
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("wc large.bin error: %v", err)
	}
	if sizeResult.ExitCode != 0 || !strings.HasPrefix(strings.TrimSpace(sizeResult.Stdout), fmt.Sprintf("%d ", len(large))) {
		t.Fatalf("large.bin size result = exit %d stdout=%q stderr=%q", sizeResult.ExitCode, sizeResult.Stdout, sizeResult.Stderr)
	}

	if err := provider.WriteFile(ctx, session.ID, "checkpoint.txt", []byte("before checkpoint\n")); err != nil {
		t.Fatalf("write checkpoint state: %v", err)
	}
	snap, err := provider.Snapshot(ctx, session.ID)
	if err != nil {
		t.Fatalf("Snapshot() error: %v", err)
	}
	if snap.ID == "" || snap.SessionID != session.ID {
		t.Fatalf("snapshot = %+v", snap)
	}

	if err := provider.WriteFile(ctx, session.ID, "checkpoint.txt", []byte("after checkpoint\n")); err != nil {
		t.Fatalf("mutate checkpoint state: %v", err)
	}
	if err := provider.Restore(ctx, session.ID, snap); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}
	restored, err := provider.ReadFile(ctx, session.ID, "checkpoint.txt")
	if err != nil {
		t.Fatalf("ReadFile after restore error: %v", err)
	}
	if string(restored) != "before checkpoint\n" {
		t.Fatalf("restored checkpoint.txt = %q", string(restored))
	}

	forked, err := provider.ForkSnapshot(ctx, session.ID, snap, forkID)
	if err != nil {
		t.Fatalf("ForkSnapshot() error: %v", err)
	}
	if forked.ID != forkID || forked.Status != "stopped" {
		t.Fatalf("forked session = %+v", forked)
	}
	startedFork, err := provider.CreateWorkspace(ctx, CreateRequest{WorktreeName: forkID})
	if err != nil {
		t.Fatalf("start forked workspace error: %v", err)
	}
	if startedFork.ID != forkID || startedFork.Status != "ready" {
		t.Fatalf("started fork = %+v", startedFork)
	}
	forkState, err := provider.ReadFile(ctx, forkID, "checkpoint.txt")
	if err != nil {
		t.Fatalf("ReadFile fork checkpoint state error: %v", err)
	}
	if string(forkState) != "before checkpoint\n" {
		t.Fatalf("fork checkpoint.txt = %q", string(forkState))
	}

	networkResult, networkErr := provider.ExecuteCommand(ctx, session.ID, Command{
		Args: []string{"git", "ls-remote", "https://github.com/github/gitignore"},
		Dir: "/workspace",
		Timeout: 10 * time.Second,
	})
	if networkErr != nil && !errors.Is(networkErr, ErrCommandTimeout) {
		t.Fatalf("network isolation command error: %v", networkErr)
	}
	if networkErr == nil && networkResult.ExitCode == 0 {
		t.Fatal("workspace unexpectedly has outbound GitHub access")
	}
}
