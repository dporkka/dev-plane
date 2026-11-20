package egresspolicy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/runtimes"
)

type fixedSessionResolver struct {
	sessionID string
	allow     bool
}

func (r fixedSessionResolver) ResolveAuthorizedSession(_ context.Context, sessionID string) (SessionIdentity, error) {
	if !r.allow || sessionID != r.sessionID {
		return SessionIdentity{}, ErrDenied
	}
	return SessionIdentity{RuntimeSessionID: r.sessionID, TenantID: "tenant", AgentID: "agent"}, nil
}

type fakeWorkspaceWriter struct {
	path  string
	data  []byte
	calls int
}

func (w *fakeWorkspaceWriter) WriteEgressReceipt(_ context.Context, _ string, receiptID string, data []byte) error {
	w.calls++
	w.path = receiptID
	w.data = append([]byte(nil), data...)
	return nil
}

func TestWorkspaceBridgeBindsTrustedSessionBeforeFetching(t *testing.T) {
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("approved")) })
	writer := &fakeWorkspaceWriter{}
	bridge := NewWorkspaceBridge(b, fixedSessionResolver{sessionID: "session-1", allow: true}, writer, NewSQLReceiptStore(receiptTestDB(t)))
	if err := bridge.FetchToWorkspace(context.Background(), "other-session", "req-1", "https://api.example.test"); !errors.Is(err, ErrDenied) {
		t.Fatalf("unbound session should be denied: %v", err)
	}
	if err := bridge.FetchToWorkspace(context.Background(), "session-1", "../escaped", "https://api.example.test"); !errors.Is(err, ErrDenied) {
		t.Fatalf("invalid delivery key should be denied: %v", err)
	}
	if writer.calls != 0 {
		t.Fatal("denied request wrote to sandbox")
	}
	if err := bridge.FetchToWorkspace(context.Background(), "session-1", "req-1", "https://api.example.test/resource"); err != nil {
		t.Fatal(err)
	}
	if writer.path != "req-1" {
		t.Fatalf("unexpected destination: %q", writer.path)
	}
	var result WorkspaceFetchReceipt
	if err := json.Unmarshal(writer.data, &result); err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != 200 || string(result.Body) != "approved" {
		t.Fatalf("unexpected receipt: %+v", result)
	}
	if err := bridge.FetchToWorkspace(context.Background(), "session-1", "req-2", "https://blocked.example.test"); !errors.Is(err, ErrDenied) {
		t.Fatalf("unallowlisted request accepted: %v", err)
	}
	if writer.calls != 1 {
		t.Fatal("blocked request wrote data")
	}

	// A newer empty snapshot revokes access without writing a receipt.
	if err := b.policies.Apply(Snapshot{Revision: 2}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.FetchToWorkspace(context.Background(), "session-1", "revoked-3", "https://api.example.test"); !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked host remained accessible: %v", err)
	}
	if writer.calls != 1 {
		t.Fatal("revocation produced a receipt")
	}
}

func TestWorkspaceBridgeFailsClosedWithoutTrustedResolver(t *testing.T) {
	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("unexpected network") })
	writer := &fakeWorkspaceWriter{}
	bridge := NewWorkspaceBridge(b, nil, writer, NewSQLReceiptStore(receiptTestDB(t)))
	if err := bridge.FetchToWorkspace(context.Background(), "session-1", "req-1", "https://api.example.test"); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing identity resolver allowed request: %v", err)
	}
	if writer.calls != 0 {
		t.Fatal("unexpected write")
	}
}

func TestDockerEgressBrokerIsolationIntegration(t *testing.T) {
	if os.Getenv("RUN_DOCKER_EGRESS_INTEGRATION") != "1" {
		t.Skip("opt in with RUN_DOCKER_EGRESS_INTEGRATION=1")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatalf("docker is required: %v", err)
	}
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Fatalf("docker daemon is required: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	image := os.Getenv("EGRESS_DOCKER_TEST_IMAGE")
	if image == "" {
		t.Fatal("EGRESS_DOCKER_TEST_IMAGE must refer to a pre-pulled local image with git, sh and curl")
	}
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Fatalf("Docker image must exist locally: %v", err)
	}
	t.Setenv("DOCKER_WORKSPACE_IMAGE", image)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-b", "main")
	runGit("config", "user.email", "egress-test@example.invalid")
	runGit("config", "user.name", "Egress Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "fixture")
	provider, err := runtimes.NewDockerProvider(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := provider.CreateWorkspace(ctx, runtimes.CreateRequest{RepositoryID: "egress-fixture", CloneURL: repo, BaseBranch: "main", Branch: "agent/isolation"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = provider.DestroyWorkspace(cleanCtx, session.ID)
	})
	name := "dev-plane-" + session.ID
	network, err := exec.Command("docker", "inspect", "-f", "{{.HostConfig.NetworkMode}}", name).CombinedOutput()
	lines := strings.Split(strings.TrimSpace(string(network)), "\n")
	if err != nil || len(lines) == 0 || strings.TrimSpace(lines[len(lines)-1]) != "none" {
		t.Fatalf("sandbox not isolated: network=%q err=%v", network, err)
	}

	// Exercise the real container, not just mocked docker CLI arguments.
	cmdResult, err := provider.ExecuteCommand(ctx, session.ID, runtimes.Command{
		UnsafeShell: true,
		Command:     "curl --noproxy '*' -k -sSf --connect-timeout 2 --max-time 3 https://1.1.1.1/ >/dev/null 2>&1",
		Timeout:     6 * time.Second,
	})
	if err != nil {
		t.Fatalf("sandbox direct egress check did not execute: %v", err)
	}
	if cmdResult.ExitCode == 0 {
		t.Fatal("isolated container unexpectedly reached network directly")
	}

	// A malicious workspace can create symlinks inside /workspace.
	poison, err := provider.ExecuteCommand(ctx, session.ID, runtimes.Command{
		UnsafeShell: true,
		Command:     "ln -s /tmp /workspace/.dev-plane && mkdir -p /tmp/egress",
	})
	if err != nil || poison.ExitCode != 0 {
		t.Fatalf("poisoned workspace fixture failed: result=%+v err=%v", poison, err)
	}

	b, _ := newBrokerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("approved-host-response"))
	})
	bridge := NewWorkspaceBridge(b, fixedSessionResolver{sessionID: session.ID, allow: true}, provider, NewSQLReceiptStore(receiptTestDB(t)))
	if err := bridge.FetchToWorkspace(ctx, session.ID, "approved-1", "https://api.example.test/fixture"); err != nil {
		t.Fatalf("brokered authorized transfer failed: %v", err)
	}
	delivery, err := provider.ExecuteCommand(ctx, session.ID, runtimes.Command{
		UnsafeShell: true,
		Command:     "cat /run/dev-plane/egress/approved-1.json",
	})
	if err != nil || delivery.ExitCode != 0 {
		t.Fatalf("read-only artifact not readable: result=%+v err=%v", delivery, err)
	}
	delivered := []byte(delivery.Stdout)
	tamper, err := provider.ExecuteCommand(ctx, session.ID, runtimes.Command{
		UnsafeShell: true,
		Command:     "echo malicious > /run/dev-plane/egress/approved-1.json",
	})
	if err != nil || tamper.ExitCode == 0 {
		t.Fatalf("sandbox overwrote host-owned artifact: result=%+v err=%v", tamper, err)
	}
	newFile, err := provider.ExecuteCommand(ctx, session.ID, runtimes.Command{
		UnsafeShell: true,
		Command:     "echo forged > /run/dev-plane/egress/forged.json",
	})
	if err != nil || newFile.ExitCode == 0 {
		t.Fatalf("sandbox created a receipt on readonly mount: result=%+v err=%v", newFile, err)
	}
	misdirected, err := provider.ExecuteCommand(ctx, session.ID, runtimes.Command{
		UnsafeShell: true,
		Command:     "test ! -e /tmp/egress/approved-1.json",
	})
	if err != nil || misdirected.ExitCode != 0 {
		t.Fatal("workspace symlink diverted privileged receipt into /tmp")
	}
	var receipt WorkspaceFetchReceipt
	if err := json.Unmarshal(delivered, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.StatusCode != 200 || string(receipt.Body) != "approved-host-response" {
		t.Fatalf("invalid delivered receipt: %+v", receipt)
	}

	if err := bridge.FetchToWorkspace(ctx, session.ID, "blocked-2", "https://not-allowed.example.test/fixture"); !errors.Is(err, ErrDenied) {
		t.Fatalf("forbidden hostname accepted: %v", err)
	}
	blocked, err := provider.ExecuteCommand(ctx, session.ID, runtimes.Command{
		UnsafeShell: true,
		Command:     "test ! -e /run/dev-plane/egress/blocked-2.json",
	})
	if err != nil || blocked.ExitCode != 0 {
		t.Fatal("denied request materialized sandbox data")
	}
}
