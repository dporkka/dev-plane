package runtimes

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestNulangLiveConformance exercises the real Nulang Cloud Workspace runtime.
// It is opt-in because it requires a reachable host-agent/KVM endpoint plus a
// repository URL that the trusted Dev Plane side can clone.
func TestNulangLiveConformance(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("NULANG_RUNTIME_URL"))
	cloneURL := strings.TrimSpace(os.Getenv("NULANG_CONFORMANCE_REPO_URL"))
	if baseURL == "" || cloneURL == "" {
		t.Skip("set NULANG_RUNTIME_URL and NULANG_CONFORMANCE_REPO_URL for live conformance")
	}
	token := os.Getenv("NULANG_RUNTIME_TOKEN")
	baseBranch := strings.TrimSpace(os.Getenv("NULANG_CONFORMANCE_BASE_BRANCH"))
	if baseBranch == "" {
		baseBranch = "main"
	}
	expectedHead := strings.TrimSpace(os.Getenv("NULANG_CONFORMANCE_EXPECTED_HEAD"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	stamp := time.Now().UTC().UnixNano()
	workspaceID := fmt.Sprintf("devplane-conformance-%d", stamp)
	provider := NewNulangCloudProvider(baseURL, token)
	req := CreateRequest{
		RepositoryID:   "nulang-live-conformance",
		CloneURL:       cloneURL,
		Branch:         fmt.Sprintf("agent/conformance/%d", stamp),
		BaseBranch:     baseBranch,
		WorktreeName:   workspaceID,
		IdempotencyKey: "conformance:" + workspaceID,
		Limits: ResourceLimits{
			CPUMillis:       2000,
			MemoryMB:        2048,
			DiskMB:          nulangWorkspaceDiskMB,
			WallTimeSeconds: 300,
		},
		Capabilities: RuntimeCapabilities{Network: false},
		Metadata: map[string]string{
			"dev_plane_task_id": "live-conformance",
			"dev_plane_run_id":  workspaceID,
		},
	}

	session, err := provider.CreateWorkspace(ctx, req)
	if err != nil {
		t.Fatalf("CreateWorkspace() error = %v", err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := provider.DestroyWorkspace(cleanupCtx, session.ID); err != nil {
			t.Logf("DestroyWorkspace cleanup error: %v", err)
		}
	}()

	// Retry the exact create identity. Cloud must replay the existing workspace,
	// not allocate another generation/logical workspace.
	replayed, err := provider.CreateWorkspace(ctx, req)
	if err != nil {
		t.Fatalf("replayed CreateWorkspace() error = %v", err)
	}
	if replayed.ID != session.ID {
		t.Fatalf("replayed workspace id = %q, want %q", replayed.ID, session.ID)
	}

	head, err := provider.ExecuteCommand(ctx, session.ID, Command{
		Args:    []string{"git", "rev-parse", "HEAD"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("git rev-parse HEAD error = %v", err)
	}
	if head.ExitCode != 0 || strings.TrimSpace(head.Stdout) == "" {
		t.Fatalf("HEAD result = %#v", head)
	}
	if expectedHead != "" && strings.TrimSpace(head.Stdout) != expectedHead {
		t.Fatalf("HEAD = %q, want %q", strings.TrimSpace(head.Stdout), expectedHead)
	}

	const markerPath = ".devplane/conformance-state.txt"
	if err := provider.WriteFile(ctx, session.ID, markerPath, []byte("before-checkpoint\n")); err != nil {
		t.Fatalf("WriteFile(before) error = %v", err)
	}
	before, err := provider.ReadFile(ctx, session.ID, markerPath)
	if err != nil || string(before) != "before-checkpoint\n" {
		t.Fatalf("ReadFile(before) = %q, %v", before, err)
	}

	patch := "diff --git a/.devplane/conformance-patch.txt b/.devplane/conformance-patch.txt\n" +
		"new file mode 100644\n" +
		"index 0000000..8e27be7\n" +
		"--- /dev/null\n" +
		"+++ b/.devplane/conformance-patch.txt\n" +
		"@@ -0,0 +1 @@\n" +
		"+patched-by-dev-plane\n"
	if err := provider.ApplyPatch(ctx, session.ID, patch); err != nil {
		t.Fatalf("ApplyPatch() error = %v", err)
	}
	patched, err := provider.ReadFile(ctx, session.ID, ".devplane/conformance-patch.txt")
	if err != nil || string(patched) != "patched-by-dev-plane\n" {
		t.Fatalf("patched file = %q, %v", patched, err)
	}

	snapshot, err := provider.Snapshot(ctx, session.ID)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.ID == "" {
		t.Fatal("Snapshot() returned empty checkpoint id")
	}

	if err := provider.WriteFile(ctx, session.ID, markerPath, []byte("after-checkpoint\n")); err != nil {
		t.Fatalf("WriteFile(after) error = %v", err)
	}
	if err := provider.Restore(ctx, session.ID, snapshot); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	restored, err := provider.ReadFile(ctx, session.ID, markerPath)
	if err != nil {
		t.Fatalf("ReadFile(restored) error = %v", err)
	}
	if string(restored) != "before-checkpoint\n" {
		t.Fatalf("restored marker = %q, want before-checkpoint", restored)
	}

	usage, err := provider.GetUsage(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.MemoryMBSeconds < 0 || usage.StorageByteSeconds < 0 || usage.CPUMilliseconds < 0 {
		t.Fatalf("usage contains negative counters: %#v", usage)
	}
	if usage.EgressBytes != 0 {
		t.Fatalf("egress_bytes = %d, want 0 while Workspace networking is disabled", usage.EgressBytes)
	}

	status, err := provider.GetStatus(ctx, session.ID)
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if status.Status != "ready" {
		t.Fatalf("status = %q, want ready", status.Status)
	}
}
