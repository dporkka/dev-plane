package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type capturePublisher struct {
	request PublishRequest
	err     error
	check   func(t *testing.T, req PublishRequest)
	t       *testing.T
}

func (p *capturePublisher) Publish(_ context.Context, req PublishRequest) error {
	p.request = req
	if p.check != nil {
		p.check(p.t, req)
	}
	return p.err
}

func TestGitPublishUsesConfiguredRemote(t *testing.T) {
	runner := &fakeRunner{}
	backend := NewGitBackend(runner)

	err := backend.Publish(context.Background(), PublishRequest{
		WorkspacePath: t.TempDir(),
		Ref:           "agent/task-42",
		Remote:        "upstream",
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"push", "-u", "upstream", "agent/task-42"}
	if got := runner.commands[0].Args; !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestJujutsuPublishUsesConfiguredRemote(t *testing.T) {
	runner := &fakeRunner{}
	backend := NewJujutsuBackend(runner)

	err := backend.Publish(context.Background(), PublishRequest{
		WorkspacePath: t.TempDir(),
		Ref:           "agent/task-42",
		Remote:        "upstream",
	})
	if err != nil {
		t.Fatal(err)
	}

	wantPush := []string{"git", "push", "--remote", "upstream", "--bookmark", "agent/task-42"}
	if got := runner.commands[1].Args; !reflect.DeepEqual(got, wantPush) {
		t.Fatalf("push args = %#v, want %#v", got, wantPush)
	}
}

func TestGitPublisherPublishesToLocalBareRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	workspace := filepath.Join(root, "workspace")

	runGitCommand(t, "", "init", "--bare", remote)
	runGitCommand(t, "", "init", "-b", "feature/test", workspace)
	runGitCommand(t, workspace, "config", "user.email", "portable@example.invalid")
	runGitCommand(t, workspace, "config", "user.name", "Portable Publisher")
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("portable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, workspace, "add", "README.md")
	runGitCommand(t, workspace, "commit", "-m", "initial")
	runGitCommand(t, workspace, "remote", "add", "upstream", remote)

	backend := NewGitBackend(nil)
	if err := backend.Publish(context.Background(), PublishRequest{
		WorkspacePath: workspace,
		Ref:           "feature/test",
		Remote:        "upstream",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	localHead := strings.TrimSpace(runGitCommand(t, workspace, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(runGitCommand(t, "", "--git-dir", remote, "rev-parse", "refs/heads/feature/test"))
	if remoteHead != localHead {
		t.Fatalf("remote head = %q, want local head %q", remoteHead, localHead)
	}
}

func runGitCommand(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func TestHTTPBasicPublisherUsesEphemeralAskPassEnvironment(t *testing.T) {
	inner := &capturePublisher{t: t}
	var askPassPath string
	inner.check = func(t *testing.T, req PublishRequest) {
		t.Helper()
		askPassPath = req.Env["GIT_ASKPASS"]
		if askPassPath == "" {
			t.Fatal("GIT_ASKPASS is missing")
		}
		if _, err := os.Stat(askPassPath); err != nil {
			t.Fatalf("askpass helper not available during publish: %v", err)
		}

		script, err := os.ReadFile(askPassPath)
		if err != nil {
			t.Fatalf("read askpass helper: %v", err)
		}
		if strings.Contains(string(script), "transport-user") || strings.Contains(string(script), "transport-secret") {
			t.Fatalf("credentials were embedded in askpass helper: %s", script)
		}
		if !strings.Contains(string(script), "DEV_PLANE_GIT_USERNAME") ||
			!strings.Contains(string(script), "DEV_PLANE_GIT_PASSWORD") {
			t.Fatalf("askpass helper does not read generic credential env vars: %s", script)
		}
		if got := req.Env["DEV_PLANE_GIT_USERNAME"]; got != "transport-user" {
			t.Fatalf("username env = %q", got)
		}
		if got := req.Env["DEV_PLANE_GIT_PASSWORD"]; got != "transport-secret" {
			t.Fatalf("password env = %q", got)
		}
	}

	publisher := NewHTTPBasicPublisher(inner, "transport-user", "transport-secret")
	err := publisher.Publish(context.Background(), PublishRequest{
		WorkspacePath: t.TempDir(),
		Ref:           "feature/test",
		Remote:        "origin",
		Env:           map[string]string{"CUSTOM": "preserved"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if inner.request.Env["CUSTOM"] != "preserved" {
		t.Fatalf("caller environment was not preserved: %#v", inner.request.Env)
	}
	if askPassPath == "" {
		t.Fatal("capture publisher did not receive askpass path")
	}
	if _, err := os.Stat(askPassPath); !os.IsNotExist(err) {
		t.Fatalf("askpass helper still exists after publish: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(askPassPath)); !os.IsNotExist(err) {
		t.Fatalf("askpass directory still exists after publish: %v", err)
	}
}

func TestHTTPBasicPublisherRejectsEmptyPassword(t *testing.T) {
	inner := &capturePublisher{t: t}
	publisher := NewHTTPBasicPublisher(inner, "user", "")

	err := publisher.Publish(context.Background(), PublishRequest{
		WorkspacePath: t.TempDir(),
		Ref:           "feature/test",
	})
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("error = %v, want password validation error", err)
	}
}
