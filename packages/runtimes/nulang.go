package runtimes

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	nulangWorkspaceFileLimit  = 8 * 1024 * 1024
	nulangWorkspaceChunkLimit = 4 * 1024 * 1024
	nulangWorkspaceDiskMB     = 10_240
	nulangMaxExecTimeout      = 50 * time.Second
)

var ErrUnsupportedRuntimeCapability = errors.New("runtime capability is not supported by provider")

// NulangRepositorySeeder materializes the repository checkout into a newly
// created Nulang Workspace. The default implementation clones on the trusted
// Dev Plane side and transfers an archive into the no-egress guest.
type NulangRepositorySeeder interface {
	Seed(ctx context.Context, provider *NulangCloudProvider, workspaceID string, req CreateRequest) error
}

// NulangCloudProvider implements Dev Plane's runtime contract against Nulang
// Cloud's first-class Workspace API. Nulang owns the Firecracker VM, durable
// filesystem/checkpoints, exact-CID guest routing, and runtime isolation.
// Dev Plane keeps repository credentials and admission policy outside the guest.
type NulangCloudProvider struct {
	*RemoteProvider
	internalToken string
	seeder        NulangRepositorySeeder
}

func NewNulangCloudProvider(baseURL, internalToken string) *NulangCloudProvider {
	remote := NewRemoteProvider(baseURL, "")
	provider := &NulangCloudProvider{
		RemoteProvider: remote,
		internalToken:  internalToken,
		seeder:         localGitNulangSeeder{},
	}
	provider.RemoteProvider.client = provider.wrapHTTPClient(&http.Client{Timeout: 120 * time.Second})
	return provider
}

func (p *NulangCloudProvider) WithHTTPClient(client *http.Client) *NulangCloudProvider {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	p.RemoteProvider.client = p.wrapHTTPClient(client)
	return p
}

func (p *NulangCloudProvider) WithRepositorySeeder(seeder NulangRepositorySeeder) *NulangCloudProvider {
	p.seeder = seeder
	return p
}

func (p *NulangCloudProvider) wrapHTTPClient(client *http.Client) *http.Client {
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &nulangAuthTransport{base: base, token: p.internalToken}
	return &clone
}

type nulangAuthTransport struct {
	base  http.RoundTripper
	token string
}

func (t *nulangAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if t.token != "" {
		clone.Header.Set("X-Internal-Auth-Token", t.token)
	}
	return t.base.RoundTrip(clone)
}

type nulangCreateWorkspaceRequest struct {
	ID       string `json:"id"`
	MemoryMB int    `json:"memory_mb"`
	VCPUs    int    `json:"vcpus"`
}

type nulangWorkspaceInfo struct {
	ID          string `json:"id,omitempty"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Status      string `json:"status"`
	MemoryMB    int    `json:"memory_mb"`
	VCPUs       int    `json:"vcpus"`
	VsockCID    int    `json:"vsock_cid"`
}

type nulangCreateWorkspaceResponse struct {
	Workspace  nulangWorkspaceInfo `json:"workspace"`
	GuestReady bool                `json:"guest_ready"`
}

type nulangWorkspaceStatusResponse struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	GuestReady bool   `json:"guest_ready"`
	MemoryMB   int    `json:"memory_mb,omitempty"`
	VCPUs      int    `json:"vcpus,omitempty"`
	VsockCID   int    `json:"vsock_cid,omitempty"`
}

type nulangWorkspaceResponse struct {
	Kind            string `json:"kind"`
	Ready           bool   `json:"ready,omitempty"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	TimedOut        bool   `json:"timed_out,omitempty"`
	StdoutBase64    string `json:"stdout_base64,omitempty"`
	StderrBase64    string `json:"stderr_base64,omitempty"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
	Path            string `json:"path,omitempty"`
	Size            uint64 `json:"size,omitempty"`
	ContentBase64   string `json:"content_base64,omitempty"`
	Offset          uint64 `json:"offset,omitempty"`
	BytesWritten    uint64 `json:"bytes_written,omitempty"`
	Code            string `json:"code,omitempty"`
	Message         string `json:"message,omitempty"`
}

type nulangCheckpoint struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"workspace_id"`
	CreatedAtMS    int64  `json:"created_at_ms"`
	FormatVersion  int    `json:"format_version"`
	RootfsSize     uint64 `json:"rootfs_size_bytes"`
	WorkspaceSize  uint64 `json:"workspace_size_bytes"`
}

type nulangCheckpointOperationResponse struct {
	Checkpoint   nulangCheckpoint `json:"checkpoint"`
	Restarted    bool             `json:"restarted"`
	RestartError string           `json:"restart_error,omitempty"`
}

func (p *NulangCloudProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	if req.Capabilities.Network || len(req.Capabilities.Secrets) > 0 {
		return nil, fmt.Errorf("%w: Nulang Workspace network/secrets are not enabled", ErrUnsupportedRuntimeCapability)
	}
	workspaceID, err := nulangWorkspaceID(req)
	if err != nil {
		return nil, err
	}
	memoryMB := req.Limits.MemoryMB
	if memoryMB <= 0 {
		memoryMB = 2048
	}
	vcpus := 1
	if req.Limits.CPUMillis > 0 {
		vcpus = (req.Limits.CPUMillis + 999) / 1000
	}
	if vcpus > 8 {
		vcpus = 8
	}
	if req.Limits.DiskMB > 0 && req.Limits.DiskMB != nulangWorkspaceDiskMB {
		return nil, fmt.Errorf("%w: Nulang Workspace disk is currently fixed at %d MiB", ErrUnsupportedRuntimeCapability, nulangWorkspaceDiskMB)
	}

	var created nulangCreateWorkspaceResponse
	err = p.doJSON(ctx, http.MethodPost, "/workspaces", nulangCreateWorkspaceRequest{
		ID: workspaceID, MemoryMB: memoryMB, VCPUs: vcpus,
	}, &created, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("create Nulang workspace: %w", err)
	}
	if !created.GuestReady {
		return nil, errors.New("create Nulang workspace: guest is not ready")
	}

	if p.seeder == nil {
		_ = p.DestroyWorkspace(ctx, workspaceID)
		return nil, errors.New("create Nulang workspace: repository seeder is required")
	}
	if err := p.seeder.Seed(ctx, p, workspaceID, req); err != nil {
		_ = p.DestroyWorkspace(ctx, workspaceID)
		return nil, fmt.Errorf("seed Nulang workspace repository: %w", err)
	}

	return &Session{
		ID:           workspaceID,
		WorkspaceID:  req.RepositoryID,
		Status:       "ready",
		Provider:     "nulang",
		WorktreePath: "",
		CreatedAt:    time.Now().UTC(),
	}, nil
}

func (p *NulangCloudProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	return p.doJSON(ctx, http.MethodDelete, "/workspaces/"+url.PathEscape(sessionID), nil, nil, "")
}

func (p *NulangCloudProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error) {
	command, args, err := normalizeNulangCommand(cmd)
	if err != nil {
		return nil, err
	}
	timeout := cmd.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > nulangMaxExecTimeout {
		return nil, fmt.Errorf("%w: Nulang unary exec supports at most %s; streaming exec is pending", ErrUnsupportedRuntimeCapability, nulangMaxExecTimeout)
	}

	var cwd *string
	if strings.TrimSpace(cmd.Dir) != "" {
		rel, err := cleanRelativePath(cmd.Dir)
		if err != nil {
			return nil, err
		}
		value := rel
		cwd = &value
	}
	return p.execRaw(ctx, sessionID, command, args, cwd, cmd.Env, timeout)
}

func (p *NulangCloudProvider) execRaw(ctx context.Context, sessionID, command string, args []string, cwd *string, env map[string]string, timeout time.Duration) (*CommandResult, error) {
	started := time.Now()
	body := map[string]any{
		"command":    command,
		"args":       args,
		"cwd":        cwd,
		"env":        env,
		"timeout_ms": timeout.Milliseconds(),
	}
	var response nulangWorkspaceResponse
	if err := p.doJSON(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(sessionID)+"/exec", body, &response, ""); err != nil {
		return nil, err
	}
	if response.Kind != "exec" {
		return nil, fmt.Errorf("unexpected Nulang exec response kind %q", response.Kind)
	}
	stdout, err := base64.StdEncoding.DecodeString(response.StdoutBase64)
	if err != nil {
		return nil, fmt.Errorf("decode Nulang stdout: %w", err)
	}
	stderr, err := base64.StdEncoding.DecodeString(response.StderrBase64)
	if err != nil {
		return nil, fmt.Errorf("decode Nulang stderr: %w", err)
	}
	if response.StdoutTruncated {
		stdout = append(stdout, []byte("\n... [Nulang stdout truncated]")...)
	}
	if response.StderrTruncated {
		stderr = append(stderr, []byte("\n... [Nulang stderr truncated]")...)
	}
	if response.TimedOut {
		return nil, fmt.Errorf("%w after %s", ErrCommandTimeout, timeout)
	}
	exitCode := -1
	if response.ExitCode != nil {
		exitCode = *response.ExitCode
	}
	return &CommandResult{
		Stdout: string(stdout), Stderr: string(stderr), ExitCode: exitCode, Duration: time.Since(started),
	}, nil
}

func (p *NulangCloudProvider) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	rel, err := cleanRelativePath(path)
	if err != nil {
		return nil, err
	}
	var response nulangWorkspaceResponse
	if err := p.doJSON(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(sessionID)+"/files/read", map[string]any{
		"path": rel, "max_bytes": nulangWorkspaceFileLimit,
	}, &response, ""); err != nil {
		return nil, err
	}
	if response.Kind != "file" {
		return nil, fmt.Errorf("unexpected Nulang read response kind %q", response.Kind)
	}
	data, err := base64.StdEncoding.DecodeString(response.ContentBase64)
	if err != nil {
		return nil, fmt.Errorf("decode Nulang file %q: %w", path, err)
	}
	return data, nil
}

func (p *NulangCloudProvider) WriteFile(ctx context.Context, sessionID, path string, data []byte) error {
	rel, err := cleanRelativePath(path)
	if err != nil {
		return err
	}
	if len(data) <= nulangWorkspaceFileLimit {
		var response nulangWorkspaceResponse
		if err := p.doJSON(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(sessionID)+"/files/write", map[string]any{
			"path": rel, "content_base64": base64.StdEncoding.EncodeToString(data), "create_parents": true,
		}, &response, ""); err != nil {
			return err
		}
		if response.Kind != "ack" {
			return fmt.Errorf("unexpected Nulang write response kind %q", response.Kind)
		}
		return nil
	}
	return p.writeFileChunks(ctx, sessionID, rel, bytes.NewReader(data))
}

func (p *NulangCloudProvider) writeFileChunks(ctx context.Context, sessionID, path string, reader io.Reader) error {
	buffer := make([]byte, nulangWorkspaceChunkLimit)
	var offset uint64
	first := true
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			final := errors.Is(readErr, io.EOF)
			var response nulangWorkspaceResponse
			if err := p.doJSON(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(sessionID)+"/files/write-chunk", map[string]any{
				"path": path, "offset": offset,
				"content_base64": base64.StdEncoding.EncodeToString(buffer[:n]),
				"truncate": first, "sync": final,
			}, &response, ""); err != nil {
				return err
			}
			if response.Kind != "chunk" && response.Kind != "ack" {
				return fmt.Errorf("unexpected Nulang chunk response kind %q", response.Kind)
			}
			offset += uint64(n)
			first = false
		}
		if errors.Is(readErr, io.EOF) {
			if offset == 0 {
				return p.WriteFile(ctx, sessionID, path, nil)
			}
			// If EOF was returned only after the final full read, send an empty
			// sync chunk so the guest fsyncs the completed file.
			if n == 0 {
				var response nulangWorkspaceResponse
				if err := p.doJSON(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(sessionID)+"/files/write-chunk", map[string]any{
					"path": path, "offset": offset, "content_base64": "", "truncate": false, "sync": true,
				}, &response, ""); err != nil {
					return err
				}
			}
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func (p *NulangCloudProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error {
	const patchPath = ".devplane/current.patch"
	if err := p.WriteFile(ctx, sessionID, patchPath, []byte(patch)); err != nil {
		return err
	}
	result, err := p.execRaw(ctx, sessionID, "git", []string{"apply", patchPath}, nil, nil, 30*time.Second)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("git apply failed: %s", strings.TrimSpace(result.Stdout+result.Stderr))
	}
	return nil
}

func (p *NulangCloudProvider) Snapshot(ctx context.Context, sessionID string) (*Snapshot, error) {
	var response nulangCheckpointOperationResponse
	if err := p.doJSON(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(sessionID)+"/checkpoints", nil, &response, ""); err != nil {
		return nil, err
	}
	createdAt := time.Now().UTC()
	if response.Checkpoint.CreatedAtMS > 0 {
		createdAt = time.UnixMilli(response.Checkpoint.CreatedAtMS).UTC()
	}
	return &Snapshot{
		ID: response.Checkpoint.ID, SessionID: sessionID,
		Description: "Nulang portable workspace checkpoint", CreatedAt: createdAt,
	}, nil
}

func (p *NulangCloudProvider) Restore(ctx context.Context, sessionID string, snap *Snapshot) error {
	if snap == nil || strings.TrimSpace(snap.ID) == "" {
		return errors.New("Nulang restore requires a checkpoint id")
	}
	path := "/workspaces/" + url.PathEscape(sessionID) + "/checkpoints/" + url.PathEscape(snap.ID) + "/restore"
	var response nulangCheckpointOperationResponse
	return p.doJSON(ctx, http.MethodPost, path, nil, &response, "")
}

func (p *NulangCloudProvider) GetStatus(ctx context.Context, sessionID string) (*SessionStatus, error) {
	var response nulangWorkspaceStatusResponse
	if err := p.doJSON(ctx, http.MethodGet, "/workspaces/"+url.PathEscape(sessionID), nil, &response, ""); err != nil {
		return nil, err
	}
	status := response.Status
	if response.Status == "running" {
		if response.GuestReady {
			status = "ready"
		} else {
			status = "pending"
		}
	}
	return &SessionStatus{SessionID: sessionID, Status: status, LastActive: time.Now().UTC()}, nil
}

func (p *NulangCloudProvider) StreamLogs(context.Context, string) (<-chan LogLine, error) {
	return nil, fmt.Errorf("%w: Nulang Workspace streaming logs are not available yet", ErrNotImplemented)
}

func (p *NulangCloudProvider) doJSON(ctx context.Context, method, path string, body any, out any, idempotencyKey string) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal Nulang request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.RemoteProvider.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key := strings.TrimSpace(idempotencyKey); key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := p.RemoteProvider.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, path)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return p.RemoteProvider.readError(resp)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode Nulang response: %w", err)
	}
	return nil
}

func nulangWorkspaceID(req CreateRequest) (string, error) {
	candidate := strings.TrimSpace(req.WorktreeName)
	if candidate == "" {
		candidate = "devplane-" + strings.TrimSpace(req.Metadata["dev_plane_run_id"])
	}
	if candidate == "devplane-" || !validNulangWorkspaceID(candidate) {
		return "", fmt.Errorf("invalid Nulang workspace id %q", candidate)
	}
	return candidate, nil
}

func validNulangWorkspaceID(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
			continue
		}
		return false
	}
	return true
}

func normalizeNulangCommand(cmd Command) (string, []string, error) {
	if len(cmd.Args) > 0 {
		if err := ValidateCommandArgs(cmd.Args); err != nil {
			return "", nil, fmt.Errorf("invalid command args: %w", err)
		}
		return cmd.Args[0], append([]string(nil), cmd.Args[1:]...), nil
	}
	if cmd.UnsafeShell {
		if strings.TrimSpace(cmd.Command) == "" {
			return "", nil, errors.New("shell command is required")
		}
		return "/bin/sh", []string{"-c", cmd.Command}, nil
	}
	args, err := ParseCommandString(cmd.Command)
	if err != nil {
		return "", nil, fmt.Errorf("invalid command: %w", err)
	}
	return args[0], args[1:], nil
}

type localGitNulangSeeder struct{}

func (localGitNulangSeeder) Seed(ctx context.Context, provider *NulangCloudProvider, workspaceID string, req CreateRequest) error {
	if strings.TrimSpace(req.CloneURL) == "" {
		return errors.New("clone URL is required")
	}
	tempDir, err := os.MkdirTemp("", "devplane-nulang-seed-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	repoDir := filepath.Join(tempDir, "repo")
	clone := exec.CommandContext(ctx, "git", "clone", req.CloneURL, repoDir)
	if output, err := clone.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w (output: %s)", err, strings.TrimSpace(string(output)))
	}
	checkout := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", "-B", req.Branch, req.BaseBranch)
	if output, err := checkout.CombinedOutput(); err != nil {
		checkout = exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", "-B", req.Branch, "origin/"+req.BaseBranch)
		if retryOutput, retryErr := checkout.CombinedOutput(); retryErr != nil {
			return fmt.Errorf("git checkout: %w (output: %s; retry: %s)", err, strings.TrimSpace(string(output)), strings.TrimSpace(string(retryOutput)))
		}
	}
	if cleanURL := credentialFreeCloneURL(req.CloneURL); cleanURL != "" {
		setURL := exec.CommandContext(ctx, "git", "-C", repoDir, "remote", "set-url", "origin", cleanURL)
		if output, err := setURL.CombinedOutput(); err != nil {
			return fmt.Errorf("sanitize git remote: %w (output: %s)", err, strings.TrimSpace(string(output)))
		}
	}
	headCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "rev-parse", "HEAD")
	headBytes, err := headCmd.Output()
	if err != nil {
		return fmt.Errorf("resolve seed HEAD: %w", err)
	}
	expectedHead := strings.TrimSpace(string(headBytes))
	if current, err := provider.execRaw(ctx, workspaceID, "git", []string{"rev-parse", "HEAD"}, nil, nil, 10*time.Second); err == nil && current.ExitCode == 0 && strings.TrimSpace(current.Stdout) == expectedHead {
		return nil
	}

	archivePath := filepath.Join(tempDir, "repository.tar")
	if err := createRepositoryTar(repoDir, archivePath); err != nil {
		return err
	}
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	if err := provider.writeFileChunks(ctx, workspaceID, ".seed/repository.tar", archive); err != nil {
		return fmt.Errorf("upload repository seed: %w", err)
	}
	result, err := provider.execRaw(ctx, workspaceID, "/bin/tar", []string{"-xf", ".seed/repository.tar", "-C", "/workspace"}, nil, nil, 30*time.Second)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("extract repository seed: %s", strings.TrimSpace(result.Stdout+result.Stderr))
	}
	_, _ = provider.execRaw(ctx, workspaceID, "/bin/rm", []string{"-f", ".seed/repository.tar"}, nil, nil, 10*time.Second)
	actual, err := provider.execRaw(ctx, workspaceID, "git", []string{"rev-parse", "HEAD"}, nil, nil, 10*time.Second)
	if err != nil {
		return err
	}
	if actual.ExitCode != 0 || strings.TrimSpace(actual.Stdout) != expectedHead {
		return fmt.Errorf("seeded repository HEAD mismatch: expected %s got %s", expectedHead, strings.TrimSpace(actual.Stdout))
	}
	return nil
}

func credentialFreeCloneURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

func createRepositoryTar(repoDir, archivePath string) error {
	file, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	writer := tar.NewWriter(file)
	walkErr := filepath.Walk(repoDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == repoDir {
			return nil
		}
		rel, err := filepath.Rel(repoDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = os.Readlink(path)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = rel
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, source)
		closeErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	closeWriterErr := writer.Close()
	closeFileErr := file.Close()
	if walkErr != nil {
		return walkErr
	}
	if closeWriterErr != nil {
		return closeWriterErr
	}
	return closeFileErr
}

var _ Provider = (*NulangCloudProvider)(nil)
