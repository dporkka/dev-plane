package agentrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
)

func TestAuthorizeToolBindsMutationApprovalToCanonicalWorkspaceTreeRevision(t *testing.T) {
	db := openAuthorityConsumptionDB(t)
	provider := &fakeRuntimeProvider{
		commandResult: &runtimes.CommandResult{Stdout: "0123456789abcdef\n", ExitCode: 0},
	}
	runner := &Runner{
		db:     db,
		kernel: capability.NewKernel(askForFileWrites(), nil, nil, slog.Default()),
		runtimes: map[string]runtimes.Provider{
			"docker": provider,
		},
	}

	sessionID := "runtime-1"
	workspace := &models.Workspace{
		ID:               "workspace-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
	}
	run := &models.AgentRun{ID: "run-1", AgentRole: models.AgentRoleImplementer}
	input := json.RawMessage(`{"path":"src/main.go","content":"package main"}`)

	err := runner.authorizeTool(context.Background(), run, nil, workspace, "", "write_file", input)
	var decision *capabilityDecisionError
	if !errors.As(err, &decision) || !decision.requiresApproval() {
		t.Fatalf("authorizeTool() error = %v, want approval-required decision", err)
	}
	if decision.result.RequestedGrant == nil {
		t.Fatal("RequestedGrant = nil")
	}
	if got, want := decision.result.RequestedGrant.Revision, "git-tree:0123456789abcdef"; got != want {
		t.Fatalf("RequestedGrant.Revision = %q, want %q", got, want)
	}
	if len(provider.commands) != 1 {
		t.Fatalf("revision commands = %d, want 1", len(provider.commands))
	}
	if !strings.Contains(provider.commands[0].Command, "GIT_INDEX_FILE") ||
		!strings.Contains(provider.commands[0].Command, "git read-tree HEAD") ||
		!strings.Contains(provider.commands[0].Command, "git add -A") ||
		!strings.Contains(provider.commands[0].Command, "git write-tree") {
		t.Fatalf("revision command is not canonical temporary-index tree capture: %q", provider.commands[0].Command)
	}
}

func TestAuthorizeToolRejectsApprovedGrantAfterWorkspaceRevisionDrifts(t *testing.T) {
	db := openAuthorityConsumptionDB(t)
	provider := &fakeRuntimeProvider{
		commandResult: &runtimes.CommandResult{Stdout: "tree-before\n", ExitCode: 0},
	}
	runner := &Runner{
		db:     db,
		kernel: capability.NewKernel(askForFileWrites(), nil, nil, slog.Default()),
		runtimes: map[string]runtimes.Provider{
			"docker": provider,
		},
	}

	sessionID := "runtime-1"
	workspace := &models.Workspace{
		ID:               "workspace-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
	}
	run := &models.AgentRun{ID: "run-1", AgentRole: models.AgentRoleImplementer}
	input := json.RawMessage(`{"path":"src/main.go","content":"package main"}`)

	err := runner.authorizeTool(context.Background(), run, nil, workspace, "", "write_file", input)
	var firstDecision *capabilityDecisionError
	if !errors.As(err, &firstDecision) || firstDecision.result.RequestedGrant == nil {
		t.Fatalf("first authorizeTool() error = %v, want revision-bound approval", err)
	}
	approved := *firstDecision.result.RequestedGrant
	insertApprovedCapabilityGrant(t, db, "approval-1", run.ID, approved)

	provider.commands = nil
	provider.commandResult = &runtimes.CommandResult{Stdout: "tree-after\n", ExitCode: 0}

	err = runner.authorizeTool(context.Background(), run, nil, workspace, "", "write_file", input)
	var driftDecision *capabilityDecisionError
	if !errors.As(err, &driftDecision) || !driftDecision.requiresApproval() {
		t.Fatalf("authorizeTool() after drift error = %v, want new approval-required decision", err)
	}
	if driftDecision.result.RequestedGrant == nil {
		t.Fatal("drift RequestedGrant = nil")
	}
	if got, want := driftDecision.result.RequestedGrant.Revision, "git-tree:tree-after"; got != want {
		t.Fatalf("drift revision = %q, want %q", got, want)
	}

	var metadata string
	if err := db.QueryRow(`SELECT metadata FROM approvals WHERE id = 'approval-1'`).Scan(&metadata); err != nil {
		t.Fatalf("load approval metadata: %v", err)
	}
	if strings.Contains(metadata, "authority_consumed_at") {
		t.Fatalf("drifted workspace consumed prior approval: %s", metadata)
	}
}

func TestAuthorizeToolFailsClosedWhenMutationRevisionCannotBeCaptured(t *testing.T) {
	db := openAuthorityConsumptionDB(t)
	provider := &fakeRuntimeProvider{
		commandResult: &runtimes.CommandResult{Stderr: "fatal: not a git repository", ExitCode: 128},
	}
	runner := &Runner{
		db:     db,
		kernel: capability.NewKernel(askForFileWrites(), nil, nil, slog.Default()),
		runtimes: map[string]runtimes.Provider{
			"docker": provider,
		},
	}

	sessionID := "runtime-1"
	workspace := &models.Workspace{
		ID:               "workspace-1",
		RuntimeProvider:  "docker",
		RuntimeSessionID: &sessionID,
	}
	run := &models.AgentRun{ID: "run-1", AgentRole: models.AgentRoleImplementer}

	err := runner.authorizeTool(
		context.Background(),
		run,
		nil,
		workspace,
		"write_file",
		json.RawMessage(`{"path":"src/main.go","content":"package main"}`),
	)
	if err == nil || !strings.Contains(err.Error(), "workspace subject revision") {
		t.Fatalf("authorizeTool() error = %v, want fail-closed revision capture error", err)
	}

	var approvals int
	if err := db.QueryRow(`SELECT COUNT(*) FROM approvals`).Scan(&approvals); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("count approvals: %v", err)
	}
	if approvals != 0 {
		t.Fatalf("approvals = %d, want 0", approvals)
	}
}

func askForFileWrites() *policies.Engine {
	return policies.NewEngine([]policies.Policy{
		{Name: "ask_file_writes", ResourceType: "file", Action: "write", Effect: policies.EffectAsk},
	})
}


func TestLocalSubjectRevisionIncludesWorkingTreeWithoutMutatingGitIndex(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}

	runGit("init")
	runGit("config", "user.email", "dev-plane@example.invalid")
	runGit("config", "user.name", "Dev Plane")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write tracked file: %v", err)
	}
	runGit("add", "tracked.txt")
	runGit("commit", "-m", "base")
	headTree := runGit("rev-parse", "HEAD^{tree}")

	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("working\n"), 0o644); err != nil {
		t.Fatalf("modify tracked file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	beforeStatus := runGit("status", "--porcelain")

	revision, err := localSubjectRevision(context.Background(), repo)
	if err != nil {
		t.Fatalf("localSubjectRevision() error = %v", err)
	}
	if !strings.HasPrefix(revision, "git-tree:") {
		t.Fatalf("revision = %q, want git-tree prefix", revision)
	}
	if revision == "git-tree:"+headTree {
		t.Fatalf("revision = committed tree %q, want working-tree content included", revision)
	}

	afterStatus := runGit("status", "--porcelain")
	if afterStatus != beforeStatus {
		t.Fatalf("git index/worktree status changed:\nbefore=%q\nafter=%q", beforeStatus, afterStatus)
	}
}
