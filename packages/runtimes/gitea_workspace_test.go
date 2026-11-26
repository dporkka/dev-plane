package runtimes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func configureCloneFixture(t *testing.T, grant GiteaWorkspaceGrant) string {
	t.Helper()
	t.Setenv("GITEA_CLONE_REPOSITORY_ID", "77")
	t.Setenv("GITEA_CLONE_URL", grant.CloneURL)
	t.Setenv("GITEA_CLONE_USERNAME", "clone-reader")
	t.Setenv("GITEA_CLONE_TOKEN", "synthetic-read-token")
	t.Setenv("GITEA_TOKEN", "synthetic-operator-api-token")
	t.Setenv("GITEA_GIT_PASSWORD", "synthetic-push-token")
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("clone-reader:synthetic-read-token"))
}

func TestScopedCloneUsesOnlyDedicatedCredential(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git required")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		cmd.Env = isolatedGitEnv(root, "", "")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git %s: %v %s", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(source, "init", "-b", "main", "--template=")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("synthetic private repository\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(source, "add", "README.md")
	run(source, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "fixture")
	want := run(source, "rev-parse", "HEAD")
	// The server is a real Git smart HTTP endpoint with a mandatory Basic auth
	// check and an independently created repository fixture.
	backend := filepath.Join(run(source, "--exec-path"), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Skip("git-http-backend required")
	}
	rootDir := filepath.Join(root, "repos")
	if err := os.MkdirAll(filepath.Join(rootDir, "acme"), 0700); err != nil {
		t.Fatal(err)
	}
	// Move the fixture's bare .git directory, rather than enabling file://.
	if err := os.Rename(filepath.Join(source, ".git"), filepath.Join(rootDir, "acme", "widget.git")); err != nil {
		t.Fatal(err)
	}
	cgiHandler := &cgi.Handler{Path: backend, Root: "", Env: []string{"GIT_PROJECT_ROOT=" + rootDir, "GIT_HTTP_EXPORT_ALL=1"}}
	var calls, bad atomic.Int64
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("clone-reader:synthetic-read-token"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != expected {
			bad.Add(1)
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		cgiHandler.ServeHTTP(w, r)
	}))
	defer server.Close()
	grant := GiteaWorkspaceGrant{RepositoryID: "repo-1", WorkspaceID: "workspace-1", InstanceURL: server.URL, CloneURL: server.URL + "/acme/widget.git", UpstreamRepositoryID: 77}
	configureCloneFixture(t, grant)
	// These inherited settings must not influence the real clone process.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.extraHeader")
	t.Setenv("GIT_CONFIG_VALUE_0", "Authorization: Basic WRONG")
	t.Setenv("GIT_TRACE_CURL", "1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("GIT_ASKPASS", "/bin/false")
	dest := filepath.Join(root, "staging")
	req := CreateRequest{RepositoryID: grant.RepositoryID, CloneURL: grant.CloneURL, BaseBranch: "main", Branch: "agent/fixture"}
	if err := cloneGiteaRepository(context.Background(), execRunner{}, dest, req, grant); err != nil {
		t.Fatal(err)
	}
	if calls.Load() == 0 || bad.Load() != 0 {
		t.Fatalf("requests=%d incorrect auth=%d", calls.Load(), bad.Load())
	}
	if got := run(dest, "rev-parse", "HEAD"); got != want {
		t.Fatalf("HEAD=%s want %s", got, want)
	}
	config, err := os.ReadFile(filepath.Join(dest, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"synthetic-read-token", "synthetic-operator-api-token", "synthetic-push-token", expected, "extraHeader", "credential.helper"} {
		if strings.Contains(string(config), forbidden) {
			t.Fatal("credential/config leaked into repository")
		}
	}
	if got := run(dest, "remote", "get-url", "origin"); got != grant.CloneURL {
		t.Fatalf("origin=%q", got)
	}
	if image := os.Getenv("DEVPLANE_TEST_GITEA_DOCKER_IMAGE"); image != "" {
		t.Run("live-container", func(t *testing.T) {
			if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
				t.Fatal("opt-in test requires an existing local trusted image")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			provider := &DockerProvider{baseDir: t.TempDir(), image: image, runner: execRunner{}, sessions: map[string]*dockerSession{}}
			session, err := provider.CreateGiteaWorkspace(ctx, req, grant)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := provider.DestroyWorkspace(context.Background(), session.ID); err != nil {
					t.Errorf("fixture cleanup: %v", err)
				}
			}()
			if err := VerifyGiteaDockerWorkspace(ctx, session.ID, grant); err != nil {
				t.Fatal(err)
			}
			data, err := provider.ReadFile(ctx, session.ID, "README.md")
			if err != nil || string(data) != "synthetic private repository\n" {
				t.Fatalf("copied fixture inaccessible: %v", err)
			}
			result, err := provider.ExecuteCommand(ctx, session.ID, Command{Command: "env", UnsafeShell: true})
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("container env check: %v", err)
			}
			for _, secret := range []string{"synthetic-read-token", "synthetic-operator-api-token", "synthetic-push-token", expected, "GITEA_TOKEN=", "GITEA_CLONE_TOKEN=", "GIT_CONFIG_VALUE_", "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY="} {
				if strings.Contains(result.Stdout, secret) {
					t.Fatal("credential leaked into container")
				}
			}
			data, err = provider.ReadFile(ctx, session.ID, ".git/config")
			if err != nil || strings.Contains(string(data), "extraHeader") || strings.Contains(string(data), "synthetic-read-token") {
				t.Fatal("container repository config not clean")
			}
			wrong := grant
			wrong.WorkspaceID = "unrelated"
			if err := VerifyGiteaDockerWorkspace(ctx, session.ID, wrong); !errors.Is(err, ErrGiteaWorkspaceAuthority) {
				t.Fatal("foreign workspace accepted")
			}
		})
	}
}

func TestScopedCloneRejectsRedirectAndMismatchedGrant(t *testing.T) {
	var unexpected, requests atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unexpected.Add(1)
		http.Error(w, "unexpected", http.StatusBadRequest)
	}))
	defer sink.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, sink.URL+r.URL.Path, http.StatusFound)
	}))
	defer source.Close()
	grant := GiteaWorkspaceGrant{RepositoryID: "repo-1", WorkspaceID: "workspace-1", InstanceURL: source.URL, CloneURL: source.URL + "/acme/widget.git", UpstreamRepositoryID: 77}
	configureCloneFixture(t, grant)
	req := CreateRequest{RepositoryID: "repo-1", CloneURL: grant.CloneURL, BaseBranch: "main", Branch: "agent/fixture"}
	ctx := context.Background()
	wrong := grant
	wrong.UpstreamRepositoryID = 88
	if err := cloneGiteaRepository(ctx, execRunner{}, filepath.Join(t.TempDir(), "wrong"), req, wrong); !errors.Is(err, ErrGiteaWorkspaceAuthority) {
		t.Fatal("wrong ID accepted")
	}
	t.Setenv("GITEA_CLONE_TOKEN", os.Getenv("GITEA_TOKEN"))
	if err := cloneGiteaRepository(ctx, execRunner{}, filepath.Join(t.TempDir(), "shared"), req, grant); !errors.Is(err, ErrGiteaWorkspaceAuthority) {
		t.Fatal("operator token reused")
	}
	if requests.Load() != 0 {
		t.Fatal("invalid grant reached upstream")
	}
	configureCloneFixture(t, grant)
	if err := cloneGiteaRepository(ctx, execRunner{}, filepath.Join(t.TempDir(), "redirect"), req, grant); !errors.Is(err, ErrGiteaWorkspaceAuthority) {
		t.Fatal("redirect accepted")
	}
	if requests.Load() == 0 || unexpected.Load() != 0 {
		t.Fatalf("redirect destination reached: %d", unexpected.Load())
	}
}

func TestGiteaContainerIsolationChecks(t *testing.T) {
	grant := GiteaWorkspaceGrant{RepositoryID: "repo-1", WorkspaceID: "workspace-1", InstanceURL: "https://git.example.test", CloneURL: "https://git.example.test/acme/widget.git", UpstreamRepositoryID: 77}
	labels := grant.labels()
	labels["dev-plane.session"] = "local-123"
	var good giteaContainerInspect
	good.Config.Labels = labels
	good.Config.Env = []string{"PATH=/usr/bin"}
	good.State.Running = true
	good.HostConfig.NetworkMode = "none"
	good.HostConfig.ReadonlyRootfs = true
	good.HostConfig.CapDrop = []string{"ALL"}
	good.HostConfig.SecurityOpt = []string{"no-new-privileges"}
	good.Mounts = append(good.Mounts, struct{ Type, Name, Destination string }{"volume", "dev-plane-local-123-workspace", "/workspace"})
	raw, err := json.Marshal(good)
	if err != nil {
		t.Fatal(err)
	}
	if !validGiteaContainer(string(raw), "local-123", grant) {
		t.Fatal("isolated fixture rejected")
	}
	for name, change := range map[string]func(*giteaContainerInspect){
		"foreign workspace":    func(c *giteaContainerInspect) { c.Config.Labels["dev-plane.gitea.workspace"] = "foreign" },
		"foreign repository":   func(c *giteaContainerInspect) { c.Config.Labels["dev-plane.gitea.repository"] = "foreign" },
		"recreated repository": func(c *giteaContainerInspect) { c.Config.Labels["dev-plane.gitea.upstream"] = "88" },
		"network":              func(c *giteaContainerInspect) { c.HostConfig.NetworkMode = "bridge" },
		"privileged":           func(c *giteaContainerInspect) { c.HostConfig.Privileged = true },
		"host bind":            func(c *giteaContainerInspect) { c.HostConfig.Binds = []string{"/:/host"} },
		"secret":               func(c *giteaContainerInspect) { c.Config.Env = append(c.Config.Env, "GITEA_TOKEN=synthetic") },
		"wrong volume":         func(c *giteaContainerInspect) { c.Mounts[0].Name = "foreign" },
	} {
		t.Run(name, func(t *testing.T) {
			var c giteaContainerInspect
			if err := json.Unmarshal(raw, &c); err != nil {
				t.Fatal(err)
			}
			change(&c)
			b, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if validGiteaContainer(string(b), "local-123", grant) {
				t.Fatal("unsafe session accepted")
			}
		})
	}
}

// A fake Docker CLI records the isolation boundary. The real authenticated
// Git transport is tested independently above; no daemon is needed in CI.
type scopedWorkspaceRunner struct {
	calls     []recordedCommand
	cloneEnvs [][]string
	labels    map[string]string
}

func (r *scopedWorkspaceRunner) Run(_ context.Context, name string, args []string, opts commandOptions) (commandOutput, error) {
	r.calls = append(r.calls, recordedCommand{name: name, args: append([]string(nil), args...)})
	if name == "git" && len(args) > 0 && args[0] == "clone" {
		r.cloneEnvs = append(r.cloneEnvs, opts.ExactEnv)
	}
	if name == "docker" && len(args) > 0 && args[0] == "run" {
		r.labels = map[string]string{}
		for i, v := range args {
			if v == "--label" && i+1 < len(args) {
				k, v, _ := strings.Cut(args[i+1], "=")
				r.labels[k] = v
			}
		}
	}
	if name == "docker" && containsArg(args, "/proc/self/status") {
		return commandOutput{Stdout: "CapInh:\t0\nCapPrm:\t0\nCapEff:\t0\nCapBnd:\t0\nCapAmb:\t0\nNoNewPrivs:\t1\n"}, nil
	}
	if name == "docker" && len(args) > 0 && args[0] == "inspect" {
		var c giteaContainerInspect
		c.Config.Labels = r.labels
		c.Config.Env = []string{"PATH=/usr/bin"}
		c.State.Running = true
		c.HostConfig.NetworkMode = "none"
		c.HostConfig.ReadonlyRootfs = true
		c.HostConfig.CapDrop = []string{"ALL"}
		c.HostConfig.SecurityOpt = []string{"no-new-privileges"}
		c.Mounts = append(c.Mounts, struct{ Type, Name, Destination string }{"volume", "dev-plane-" + r.labels["dev-plane.session"] + "-workspace", "/workspace"})
		b, err := json.Marshal(c)
		if err != nil {
			return commandOutput{}, err
		}
		return commandOutput{Stdout: string(b)}, nil
	}
	return commandOutput{}, nil
}
func (r *scopedWorkspaceRunner) Start(context.Context, string, []string, commandOptions) (startedCommand, error) {
	return nil, errors.New("unexpected process")
}

func TestCreateGiteaWorkspaceUsesScopedCloneAndIsolatedContainer(t *testing.T) {
	grant := GiteaWorkspaceGrant{RepositoryID: "repo-1", WorkspaceID: "workspace-1", InstanceURL: "https://git.example.test", CloneURL: "https://git.example.test/acme/widget.git", UpstreamRepositoryID: 77}
	header := configureCloneFixture(t, grant)
	runner := &scopedWorkspaceRunner{}
	provider := &DockerProvider{baseDir: t.TempDir(), image: "trusted-image", runner: runner, sessions: map[string]*dockerSession{}}
	req := CreateRequest{RepositoryID: "repo-1", CloneURL: grant.CloneURL, BaseBranch: "main", Branch: "agent/test"}
	session, err := provider.CreateGiteaWorkspace(context.Background(), req, grant)
	if err != nil || session == nil || session.Provider != "docker" || session.WorktreePath != "" {
		t.Fatalf("scoped workspace failed: %v", err)
	}
	if len(runner.cloneEnvs) != 1 || !strings.Contains(strings.Join(runner.cloneEnvs[0], "\n"), header) {
		t.Fatal("scoped credential missing from clone")
	}
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		for _, v := range []string{header, "synthetic-read-token", "synthetic-operator-api-token", "synthetic-push-token"} {
			if strings.Contains(joined, v) {
				t.Fatal("credential passed in process arguments")
			}
		}
		if call.name == "docker" && containsArg(call.args, "-e") {
			t.Fatal("credential or arbitrary env forwarded to container")
		}
	}
	if runner.labels["dev-plane.gitea.workspace"] != "workspace-1" || runner.labels["dev-plane.gitea.upstream"] != "77" {
		t.Fatal("missing immutable identity labels")
	}
	calls := len(runner.calls)
	req.Env = map[string]string{"GITEA_TOKEN": "synthetic"}
	if _, err := provider.CreateGiteaWorkspace(context.Background(), req, grant); !errors.Is(err, ErrGiteaWorkspaceAuthority) {
		t.Fatal("arbitrary env accepted")
	}
	if len(runner.calls) != calls {
		t.Fatal("invalid request reached runtime")
	}
}

func TestCapabilityStatusRejectsIncompleteOrPrivilegedExec(t *testing.T) {
	good := "CapInh: 0\nCapPrm: 0\nCapEff: 0\nCapBnd: 0\nCapAmb: 0\nNoNewPrivs: 1\n"
	if !validCapabilityStatus(good) {
		t.Fatal("zero capabilities rejected")
	}
	for _, bad := range []string{strings.Replace(good, "CapBnd: 0", "CapBnd: 1", 1), strings.Replace(good, "NoNewPrivs: 1", "NoNewPrivs: 0", 1), strings.Replace(good, "CapAmb: 0\n", "", 1), good + "CapEff: 0\n"} {
		if validCapabilityStatus(bad) {
			t.Fatal("unsafe capability status accepted")
		}
	}
}
