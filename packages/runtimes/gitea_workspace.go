package runtimes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrGiteaWorkspaceAuthority omits credentials, Git output and inspection data.
var ErrGiteaWorkspaceAuthority = errors.New("gitea workspace authority is unavailable")

// GiteaWorkspaceGrant contains only verified identity, never credentials.
// It is not part of the remote workspace API.
type GiteaWorkspaceGrant struct {
	RepositoryID, WorkspaceID, InstanceURL, CloneURL string
	UpstreamRepositoryID, AuthorizationGeneration    int64
}

func (g GiteaWorkspaceGrant) valid() bool {
	if g.RepositoryID == "" || g.WorkspaceID == "" || g.InstanceURL == "" || g.UpstreamRepositoryID <= 0 || g.AuthorizationGeneration <= 0 ||
		!strings.HasPrefix(g.CloneURL, g.InstanceURL+"/") || !strings.HasSuffix(g.CloneURL, ".git") {
		return false
	}
	root, err := url.Parse(g.InstanceURL)
	if err != nil || root.Host == "" || root.User != nil || root.RawQuery != "" || root.Fragment != "" || root.ForceQuery {
		return false
	}
	u, err := url.Parse(g.CloneURL)
	if err != nil || u.Host != root.Host || u.Scheme != root.Scheme || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || u.ForceQuery || u.RawPath != "" || strings.ContainsAny(g.CloneURL, "\r\n\x00") {
		return false
	}
	// Cleartext is restricted to disposable loopback qualification.
	ip := net.ParseIP(root.Hostname())
	return u.Scheme == "https" || (u.Scheme == "http" && ip != nil && ip.IsLoopback())
}

func (g GiteaWorkspaceGrant) labels() map[string]string {
	return map[string]string{
		"dev-plane.gitea.workspace":  g.WorkspaceID,
		"dev-plane.gitea.repository": g.RepositoryID,
		"dev-plane.gitea.instance":   g.InstanceURL,
		"dev-plane.gitea.generation": strconv.FormatInt(g.AuthorizationGeneration, 10),
		"dev-plane.gitea.upstream":   strconv.FormatInt(g.UpstreamRepositoryID, 10),
	}
}

func giteaCloneAuthorization(g GiteaWorkspaceGrant) (string, error) {
	id, err := strconv.ParseInt(os.Getenv("GITEA_CLONE_REPOSITORY_ID"), 10, 64)
	user, token := os.Getenv("GITEA_CLONE_USERNAME"), os.Getenv("GITEA_CLONE_TOKEN")
	if !g.valid() || err != nil || id != g.UpstreamRepositoryID || os.Getenv("GITEA_BINDING_GENERATION") != strconv.FormatInt(g.AuthorizationGeneration, 10) || os.Getenv("GITEA_CLONE_URL") != g.CloneURL ||
		user == "" || token == "" || strings.TrimSpace(user) != user || strings.TrimSpace(token) != token ||
		strings.ContainsAny(user, ":\r\n\x00") || strings.ContainsAny(token, "\r\n\x00") ||
		token == os.Getenv("GITEA_TOKEN") || token == os.Getenv("GITEA_GIT_PASSWORD") {
		return "", ErrGiteaWorkspaceAuthority
	}
	return "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token)), nil
}

// Only operator PATH and an empty temporary home are inherited. The credential
// is present solely in the URL-scoped Git config environment for clone.
func isolatedGitEnv(home, cloneURL, authorization string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home,
		"LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false", "SSH_ASKPASS=/bin/false"}
	config := [][2]string{{"credential.helper", ""}, {"core.hooksPath", "/dev/null"},
		{"http.followRedirects", "false"}, {"http.proxy", ""}, {"http.sslVerify", "true"},
		{"protocol.allow", "never"}, {"protocol.https.allow", "always"}, {"protocol.http.allow", "always"}}
	if authorization != "" {
		config = append(config, [2]string{"http." + cloneURL + ".extraHeader", authorization})
	}
	env = append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(len(config)))
	for i, pair := range config {
		n := strconv.Itoa(i)
		env = append(env, "GIT_CONFIG_KEY_"+n+"="+pair[0], "GIT_CONFIG_VALUE_"+n+"="+pair[1])
	}
	return env
}

// Clone runs on the trusted Docker host before starting the container. The
// read credential never goes in argv, local config, or workspace environment.
func cloneGiteaRepository(ctx context.Context, runner commandRunner, repoDir string, req CreateRequest, grant GiteaWorkspaceGrant) error {
	auth, err := giteaCloneAuthorization(grant)
	if err != nil || req.CloneURL != grant.CloneURL || req.RepositoryID != grant.RepositoryID || len(req.Env) != 0 {
		return ErrGiteaWorkspaceAuthority
	}
	home, err := os.MkdirTemp(filepath.Dir(repoDir), "git-home-")
	if err != nil {
		return ErrGiteaWorkspaceAuthority
	}
	defer func() { _ = os.RemoveAll(home) }()
	clean := commandOptions{ExactEnv: isolatedGitEnv(home, "", "")}
	for _, branch := range []string{req.BaseBranch, req.Branch} {
		if branch == "" || strings.HasPrefix(branch, "-") {
			return ErrGiteaWorkspaceAuthority
		}
		if _, err = runner.Run(ctx, "git", []string{"check-ref-format", "refs/heads/" + branch}, clean); err != nil {
			return ErrGiteaWorkspaceAuthority
		}
	}
	if _, err = runner.Run(ctx, "git", []string{"clone", "--no-checkout", "--no-recurse-submodules", "--template=", "--", grant.CloneURL, repoDir},
		commandOptions{ExactEnv: isolatedGitEnv(home, grant.CloneURL, auth)}); err != nil {
		return fmt.Errorf("%w: scoped clone failed", ErrGiteaWorkspaceAuthority)
	}
	if _, err = runner.Run(ctx, "git", []string{"-C", repoDir, "checkout", "-B", req.Branch, "refs/remotes/origin/" + req.BaseBranch, "--"}, clean); err != nil {
		return fmt.Errorf("%w: scoped checkout failed", ErrGiteaWorkspaceAuthority)
	}
	return nil
}

// CreateGiteaWorkspace provisions a credential-free Docker workspace using an
// independently scoped read-only clone credential selected by the operator.
func (p *DockerProvider) CreateGiteaWorkspace(ctx context.Context, req CreateRequest, grant GiteaWorkspaceGrant) (*Session, error) {
	if _, err := giteaCloneAuthorization(grant); err != nil || req.RepositoryID != grant.RepositoryID ||
		req.CloneURL != grant.CloneURL || len(req.Env) != 0 {
		return nil, ErrGiteaWorkspaceAuthority
	}
	return p.createWorkspace(ctx, req, &grant)
}

// Preserve only operator-selected local runtime connectivity. Podman can
// automatically copy inherited proxy settings into containers, so do not
// pass the worker environment to scoped Docker commands.
func giteaDockerEnv() []string {
	env := []string{}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "DBUS_SESSION_BUS_ADDRESS", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH", "CONTAINER_HOST", "CONTAINER_SSHKEY"} {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}

type giteaContainerInspect struct {
	Config struct {
		Labels map[string]string
		Env    []string
	}
	State      struct{ Running bool }
	HostConfig struct {
		NetworkMode, PidMode, IpcMode       string
		ReadonlyRootfs, Privileged          bool
		CapDrop, CapAdd, SecurityOpt, Binds []string
		Devices, DeviceRequests             []json.RawMessage
	}
	Mounts []struct{ Type, Name, Destination string }
}

func validGiteaContainer(raw, sessionID string, grant GiteaWorkspaceGrant) bool {
	var container giteaContainerInspect
	if json.Unmarshal([]byte(raw), &container) != nil || !container.State.Running {
		return false
	}
	labels := grant.labels()
	labels["dev-plane.session"] = sessionID
	for k, v := range labels {
		if container.Config.Labels[k] != v {
			return false
		}
	}
	h := container.HostConfig
	if h.NetworkMode != "none" || !h.ReadonlyRootfs || h.Privileged || h.PidMode == "host" || h.IpcMode == "host" ||
		len(h.CapAdd) != 0 || len(h.Devices) != 0 || len(h.DeviceRequests) != 0 {
		return false
	}
	has := func(values []string, want string) bool {
		for _, v := range values {
			if v == want {
				return true
			}
		}
		return false
	}
	if len(h.CapDrop) == 0 || (!has(h.SecurityOpt, "no-new-privileges") && !has(h.SecurityOpt, "no-new-privileges:true")) {
		return false
	}
	expectedVolume := "dev-plane-" + sessionID + "-workspace"
	// Podman reports named volume mounts in Binds. Accept exactly the
	// expected named volume, not a host bind or additional mount.
	if len(h.Binds) > 1 {
		return false
	}
	if len(h.Binds) == 1 {
		prefix := expectedVolume + ":" + workspaceDir + ":"
		if !strings.HasPrefix(h.Binds[0], prefix) {
			return false
		}
		for _, option := range strings.Split(strings.TrimPrefix(h.Binds[0], prefix), ",") {
			switch option {
			case "rprivate", "private", "nosuid", "nodev", "rbind", "rw":
			default:
				return false
			}
		}
	}
	volumeFound := false
	for _, mount := range container.Mounts {
		switch {
		case mount.Type == "volume" && mount.Name == "dev-plane-"+sessionID+"-workspace" && mount.Destination == workspaceDir && !volumeFound:
			volumeFound = true
		case mount.Type == "tmpfs" && mount.Destination == "/tmp":
		default:
			return false
		}
	}
	if !volumeFound {
		return false
	}
	for _, entry := range container.Config.Env {
		key, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GITEA_") || strings.HasPrefix(upper, "DEV_PLANE_GIT_") || strings.HasPrefix(upper, "GIT_CONFIG_") {
			return false
		}
		switch upper {
		case "GITHUB_TOKEN", "SSH_AUTH_SOCK", "GIT_ASKPASS", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
			return false
		}
	}
	return true
}

// VerifyGiteaDockerWorkspace verifies the actual container, not just a DB
// runtime_provider string or a caller-controlled session identifier.
func VerifyGiteaDockerWorkspace(ctx context.Context, sessionID string, grant GiteaWorkspaceGrant) error {
	return verifyGiteaDockerWorkspace(ctx, execRunner{}, sessionID, grant)
}

func verifyGiteaDockerWorkspace(ctx context.Context, runner commandRunner, sessionID string, grant GiteaWorkspaceGrant) error {
	if !grant.valid() || !strings.HasPrefix(sessionID, "local-") {
		return ErrGiteaWorkspaceAuthority
	}
	if _, err := strconv.ParseUint(strings.TrimPrefix(sessionID, "local-"), 10, 64); err != nil {
		return ErrGiteaWorkspaceAuthority
	}
	opts := commandOptions{ExactEnv: giteaDockerEnv()}
	out, err := runner.Run(ctx, "docker", []string{"inspect", "--type", "container", "--format", "{{json .}}", "dev-plane-" + sessionID}, opts)
	if err != nil || !validGiteaContainer(out.Stdout, sessionID, grant) {
		return ErrGiteaWorkspaceAuthority
	}
	// Docker reports ALL, Podman reports individual drops. Verify the
	// kernel's actual effective, permitted and bounding capability sets
	// from a fresh exec, and no_new_privs, rather than guessing from names.
	status, err := runner.Run(ctx, "docker", []string{"exec", "dev-plane-" + sessionID, "/bin/cat", "/proc/self/status"}, opts)
	if err != nil || !validCapabilityStatus(status.Stdout) {
		return ErrGiteaWorkspaceAuthority
	}
	return nil
}

func validCapabilityStatus(raw string) bool {
	want := map[string]string{"CapInh": "0", "CapPrm": "0", "CapEff": "0", "CapBnd": "0", "CapAmb": "0", "NoNewPrivs": "1"}
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		expected, needed := want[key]
		if !needed {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		if err != nil || seen[key] || strconv.FormatUint(n, 16) != expected {
			return false
		}
		seen[key] = true
	}
	return len(seen) == len(want)
}
