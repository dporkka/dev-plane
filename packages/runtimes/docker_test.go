package runtimes

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

type recordedCommand struct {
	name  string
	args  []string
	stdin string
}

type fakeRunner struct {
	calls []recordedCommand
}

func (r *fakeRunner) Run(ctx context.Context, name string, args []string, opts commandOptions) (commandOutput, error) {
	rec := recordedCommand{name: name, args: append([]string(nil), args...)}
	if opts.Stdin != nil {
		data, _ := io.ReadAll(opts.Stdin)
		rec.stdin = string(data)
	}
	r.calls = append(r.calls, rec)

	if name == "docker" && len(args) >= 3 && args[0] == "inspect" {
		return commandOutput{Stdout: "running 1234\n"}, nil
	}
	if name == "docker" && containsArg(args, "cat -- \"$1\"") {
		return commandOutput{Stdout: "file contents"}, nil
	}
	if name == "docker" && containsArg(args, "rev-parse") {
		return commandOutput{Stdout: "abc123\n"}, nil
	}
	return commandOutput{ExitCode: 0, Duration: time.Millisecond}, nil
}

func (r *fakeRunner) Start(ctx context.Context, name string, args []string, opts commandOptions) (startedCommand, error) {
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte("log line\n"))
	}
	return fakeStartedCommand{}, nil
}

type fakeStartedCommand struct{}

func (fakeStartedCommand) Wait() error { return nil }

func TestDockerProviderCreateWorkspaceUsesIsolatedContainer(t *testing.T) {
	runner := &fakeRunner{}
	provider := &DockerProvider{
		baseDir:  t.TempDir(),
		image:    "test-image:latest",
		runner:   runner,
		sessions: map[string]*dockerSession{},
	}

	session, err := provider.CreateWorkspace(context.Background(), CreateRequest{
		RepositoryID: "repo-1",
		CloneURL:     "https://example.invalid/repo.git",
		Branch:       "feature/test",
		BaseBranch:   "main",
		Env:          map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("CreateWorkspace() error: %v", err)
	}
	if session.Provider != "docker" {
		t.Fatalf("Provider = %q, want docker", session.Provider)
	}
	if session.Status != "ready" {
		t.Fatalf("Status = %q, want ready", session.Status)
	}

	clone := findCall(runner.calls, "git", "clone")
	if clone == nil {
		t.Fatal("expected git clone call")
	}
	run := findCall(runner.calls, "docker", "run")
	if run == nil {
		t.Fatal("expected docker run call")
	}
	for _, want := range []string{
		"--network", "none",
		"--read-only",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=64m",
		"--mount",
		"--memory", defaultDockerMemory,
		"--cpus", defaultDockerCPUs,
		"--pids-limit", defaultDockerPIDs,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--workdir", workspaceDir,
		"--entrypoint", "sh",
		"-e", "FOO=bar",
		"test-image:latest",
	} {
		if !containsArg(run.args, want) {
			t.Fatalf("docker run args missing %q: %v", want, run.args)
		}
	}
	if !containsArgPrefix(run.args, "type=volume,source=dev-plane-") {
		t.Fatalf("docker run args missing workspace volume mount: %v", run.args)
	}
	if !containsArgPrefix(run.args, "--label") || !containsArgPrefix(run.args, "dev-plane.session=") {
		t.Fatalf("docker run args missing session label: %v", run.args)
	}
	cp := findCall(runner.calls, "docker", "cp")
	if cp == nil {
		t.Fatal("expected docker cp call")
	}
	if !strings.Contains(strings.Join(cp.args, " "), ":"+workspaceDir+"/") {
		t.Fatalf("docker cp did not target workspace dir: %v", cp.args)
	}
}

func TestDockerProviderAttachSessionReconstructsDeterministicNames(t *testing.T) {
	runner := &fakeRunner{}
	provider := &DockerProvider{
		baseDir:  t.TempDir(),
		image:    "test-image:latest",
		runner:   runner,
		sessions: map[string]*dockerSession{},
	}

	session, err := provider.AttachSession(context.Background(), "sess-attach", "workspace-1")
	if err != nil {
		t.Fatalf("AttachSession() error: %v", err)
	}
	if session.ID != "sess-attach" || session.Provider != "docker" || session.Status != "ready" {
		t.Fatalf("session = %+v, want attached docker ready session", session)
	}

	if _, err := provider.ExecuteCommand(context.Background(), "sess-attach", Command{Command: "pwd"}); err != nil {
		t.Fatalf("ExecuteCommand() after attach error: %v", err)
	}
	call := findCall(runner.calls, "docker", "exec")
	if call == nil {
		t.Fatal("expected docker exec call after attach")
	}
	if !containsArg(call.args, "dev-plane-sess-attach") {
		t.Fatalf("docker exec args = %v, want deterministic attached container name", call.args)
	}
}

func TestDockerProviderExecuteCommandUsesContainerWorkdirAndEnv(t *testing.T) {
	runner := &fakeRunner{}
	provider := dockerProviderWithSession(t, runner)

	result, err := provider.ExecuteCommand(context.Background(), "sess-1", Command{
		Command: "go test ./...",
		Dir:     "src/app",
		Env:     map[string]string{"GOFLAGS": "-count=1"},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand() error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", result.ExitCode)
	}

	call := findCall(runner.calls, "docker", "exec")
	if call == nil {
		t.Fatal("expected docker exec call")
	}
	for _, want := range []string{"-w", workspaceDir + "/src/app", "-e", "GOFLAGS=-count=1", "container-1", "sh", "-c", "go test ./..."} {
		if !containsArg(call.args, want) {
			t.Fatalf("docker exec args missing %q: %v", want, call.args)
		}
	}
}

func TestDockerProviderRejectsUnsafePaths(t *testing.T) {
	provider := dockerProviderWithSession(t, &fakeRunner{})

	if _, err := provider.ReadFile(context.Background(), "sess-1", "/etc/passwd"); err == nil {
		t.Fatal("ReadFile absolute path error = nil, want error")
	}
	if err := provider.WriteFile(context.Background(), "sess-1", "../outside", []byte("x")); err == nil {
		t.Fatal("WriteFile traversal error = nil, want error")
	}
	if _, err := provider.ExecuteCommand(context.Background(), "sess-1", Command{Command: "pwd", Dir: "../../host"}); err == nil {
		t.Fatal("ExecuteCommand traversal dir error = nil, want error")
	}
}

func TestDockerProviderFilePatchSnapshotAndStatusCommands(t *testing.T) {
	runner := &fakeRunner{}
	provider := dockerProviderWithSession(t, runner)

	data, err := provider.ReadFile(context.Background(), "sess-1", "README.md")
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if string(data) != "file contents" {
		t.Fatalf("ReadFile() = %q, want file contents", string(data))
	}
	if err := provider.WriteFile(context.Background(), "sess-1", "dir/file.txt", []byte("hello")); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	if err := provider.ApplyPatch(context.Background(), "sess-1", "diff --git a/a b/a\n"); err != nil {
		t.Fatalf("ApplyPatch() error: %v", err)
	}
	snap, err := provider.Snapshot(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Snapshot() error: %v", err)
	}
	if snap.GitCommit != "abc123" {
		t.Fatalf("GitCommit = %q, want abc123", snap.GitCommit)
	}
	status, err := provider.GetStatus(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("GetStatus() error: %v", err)
	}
	if status.Status != "ready" || status.PID != 1234 {
		t.Fatalf("status = %+v, want ready pid 1234", status)
	}

	write := findCallWithArg(runner.calls, "docker", `cat > "$1"`)
	if write == nil || write.stdin != "hello" {
		t.Fatalf("write call/stdin = %+v, want hello stdin", write)
	}
	patch := findCallWithArg(runner.calls, "docker", "apply")
	if patch == nil || patch.stdin == "" {
		t.Fatalf("patch call/stdin missing: %+v", patch)
	}
}

func dockerProviderWithSession(t *testing.T, runner *fakeRunner) *DockerProvider {
	t.Helper()
	now := time.Now()
	return &DockerProvider{
		baseDir: t.TempDir(),
		image:   "test-image:latest",
		runner:  runner,
		sessions: map[string]*dockerSession{
			"sess-1": {
				id:          "sess-1",
				workspaceID: "repo-1",
				container:   "container-1",
				volume:      "volume-1",
				status:      "ready",
				createdAt:   now,
				lastActive:  now,
			},
		},
	}
}

func findCall(calls []recordedCommand, name, firstArg string) *recordedCommand {
	for i := range calls {
		if calls[i].name == name && len(calls[i].args) > 0 && calls[i].args[0] == firstArg {
			return &calls[i]
		}
	}
	return nil
}

func findCallWithArg(calls []recordedCommand, name, arg string) *recordedCommand {
	for i := range calls {
		if calls[i].name == name && containsArg(calls[i].args, arg) {
			return &calls[i]
		}
	}
	return nil
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func containsArgPrefix(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}
