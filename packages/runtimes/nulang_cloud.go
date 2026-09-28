package runtimes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
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

// NulangCloudProvider adapts Dev Plane's runtime-neutral Provider contract to
// Nulang Cloud's authenticated Workspace API. Firecracker/vsock details remain
// entirely behind Nulang Cloud.
type NulangCloudProvider struct {
	baseURL string
	token   string
	client  *http.Client
	cloneRepository func(context.Context, CreateRequest) (string, func(), error)
}

func NewNulangCloudProvider(baseURL, token string) *NulangCloudProvider {
	return &NulangCloudProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		token: token,
		client: &http.Client{Timeout: 120 * time.Second},
		cloneRepository: cloneRepositoryForSeed,
	}
}

func (p *NulangCloudProvider) WithHTTPClient(client *http.Client) *NulangCloudProvider {
	p.client = client
	return p
}

func (p *NulangCloudProvider) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil { return nil, err }
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, reader)
	if err != nil { return nil, err }
	if body != nil { req.Header.Set("Content-Type", "application/json") }
	if p.token != "" { req.Header.Set("Authorization", "Bearer "+p.token) }
	return p.client.Do(req)
}

func (p *NulangCloudProvider) apiError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := strings.TrimSpace(string(data))
	if resp.StatusCode == http.StatusNotFound { return ErrSessionNotFound }
	if msg == "" { msg = http.StatusText(resp.StatusCode) }
	return fmt.Errorf("nulang cloud returned %d: %s", resp.StatusCode, msg)
}

func workspaceID(req CreateRequest) string {
	id := req.WorktreeName
	if id == "" { id = req.RepositoryID }
	id = strings.ToLower(id)
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	id = strings.Trim(b.String(), "-")
	if id == "" { id = fmt.Sprintf("devplane-%d", time.Now().UnixNano()) }
	return id
}

func (p *NulangCloudProvider) CreateWorkspace(ctx context.Context, req CreateRequest) (*Session, error) {
	id := workspaceID(req)
	resp, err := p.request(ctx, http.MethodPost, "/workspaces", map[string]any{"id": id})
	if err != nil { return nil, fmt.Errorf("create Nulang Cloud workspace: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, p.apiError(resp) }
	var out struct {
		Workspace struct {
			WorkspaceID string `json:"workspace_id"`
			Status string `json:"status"`
		} `json:"workspace"`
		GuestReady bool `json:"guest_ready"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	status := out.Workspace.Status
	if out.GuestReady { status = "ready" }
	session := &Session{ID: out.Workspace.WorkspaceID, WorkspaceID: out.Workspace.WorkspaceID, Status: status, Provider: "nulang-cloud", CreatedAt: time.Now().UTC()}
	if req.CloneURL != "" {
		if err := p.seedRepository(ctx, session.ID, req); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = p.DestroyWorkspace(cleanupCtx, session.ID)
			cancel()
			return nil, fmt.Errorf("seed Nulang Cloud workspace: %w", err)
		}
	}
	return session, nil
}

func (p *NulangCloudProvider) DestroyWorkspace(ctx context.Context, id string) error {
	resp, err := p.request(ctx, http.MethodDelete, "/workspaces/"+url.PathEscape(id), nil)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return p.apiError(resp) }
	return nil
}

func (p *NulangCloudProvider) ExecuteCommand(ctx context.Context, id string, cmd Command) (*CommandResult, error) {
	return p.executeCommand(ctx, id, cmd, false)
}

func (p *NulangCloudProvider) executeInternalCommand(ctx context.Context, id string, cmd Command) (*CommandResult, error) {
	return p.executeCommand(ctx, id, cmd, true)
}

func (p *NulangCloudProvider) executeCommand(ctx context.Context, id string, cmd Command, trustedInternal bool) (*CommandResult, error) {
	var command string
	var args []string
	if len(cmd.Args) > 0 {
		if !trustedInternal {
			if err := ValidateCommandArgs(cmd.Args); err != nil {
				return nil, fmt.Errorf("invalid command args: %w", err)
			}
		}
		command, args = cmd.Args[0], cmd.Args[1:]
	} else {
		if cmd.Command == "" { return nil, fmt.Errorf("command is required") }
		if !trustedInternal && !cmd.UnsafeShell {
			if _, err := ParseCommandString(cmd.Command); err != nil {
				return nil, fmt.Errorf("invalid command: %w", err)
			}
		}
		command, args = "sh", []string{"-c", cmd.Command}
	}
	timeoutMS := int64(0)
	if cmd.Timeout > 50*time.Second { return nil, fmt.Errorf("command timeout %s exceeds Nulang Cloud maximum of 50s", cmd.Timeout) }
	if cmd.Timeout > 0 { timeoutMS = cmd.Timeout.Milliseconds() }
	body := map[string]any{"command": command, "args": args, "env": cmd.Env}
	if cmd.Dir != "" { body["cwd"] = cmd.Dir }
	if timeoutMS > 0 { body["timeout_ms"] = timeoutMS }
	started := time.Now()
	resp, err := p.request(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(id)+"/exec", body)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, p.apiError(resp) }
	var out struct {
		Kind string `json:"kind"`
		ExitCode *int `json:"exit_code"`
		TimedOut bool `json:"timed_out"`
		Stdout string `json:"stdout_base64"`
		Stderr string `json:"stderr_base64"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	if out.TimedOut { return nil, ErrCommandTimeout }
	stdout, err := base64.StdEncoding.DecodeString(out.Stdout); if err != nil { return nil, err }
	stderr, err := base64.StdEncoding.DecodeString(out.Stderr); if err != nil { return nil, err }
	exit := 0; if out.ExitCode != nil { exit = *out.ExitCode }
	return &CommandResult{Stdout:string(stdout), Stderr:string(stderr), ExitCode:exit, Duration:time.Since(started)}, nil
}

func (p *NulangCloudProvider) ReadFile(ctx context.Context, id, path string) ([]byte, error) {
	resp, err := p.request(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(id)+"/files/read", map[string]any{"path": path, "max_bytes": nulangCloudSingleFileBytes})
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, p.apiError(resp) }
	// content_base64 needs an explicit tag because the wire contract is snake_case.
	var wire struct { Content string `json:"content_base64"` }
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil { return nil, err }
	return base64.StdEncoding.DecodeString(wire.Content)
}

func (p *NulangCloudProvider) WriteFile(ctx context.Context, id, path string, data []byte) error {
	if len(data) > nulangCloudSingleFileBytes {
		return p.uploadFileChunks(ctx, id, path, bytes.NewReader(data))
	}
	resp, err := p.request(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(id)+"/files/write", map[string]any{"path": path, "content_base64": base64.StdEncoding.EncodeToString(data), "create_parents": true})
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return p.apiError(resp) }
	return nil
}

func (p *NulangCloudProvider) ApplyPatch(ctx context.Context, id, patch string) error {
	const patchPath = ".devplane/patch.diff"
	if err := p.WriteFile(ctx, id, patchPath, []byte(patch)); err != nil { return err }
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = p.executeInternalCommand(cleanupCtx, id, Command{Args: []string{"rm", "-f", patchPath}, Dir: "/workspace"})
	}()
	result, err := p.ExecuteCommand(ctx, id, Command{Args: []string{"git", "apply", "--whitespace=nowarn", patchPath}, Dir: "/workspace"})
	if err != nil { return err }
	if result.ExitCode != 0 { return fmt.Errorf("git apply failed: %s", strings.TrimSpace(result.Stderr)) }
	return nil
}

func (p *NulangCloudProvider) Snapshot(ctx context.Context, id string) (*Snapshot, error) {
	resp, err := p.request(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(id)+"/checkpoints", map[string]any{})
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, p.apiError(resp) }
	var out struct { Checkpoint struct { ID string `json:"id"`; WorkspaceID string `json:"workspace_id"`; CreatedAtMS int64 `json:"created_at_ms"` } `json:"checkpoint"` }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	return &Snapshot{ID:out.Checkpoint.ID, SessionID:out.Checkpoint.WorkspaceID, Description:"Nulang Cloud portable filesystem checkpoint", CreatedAt:time.UnixMilli(out.Checkpoint.CreatedAtMS).UTC()}, nil
}

func (p *NulangCloudProvider) Restore(ctx context.Context, id string, snap *Snapshot) error {
	if snap == nil || snap.ID == "" { return fmt.Errorf("snapshot id is required") }
	if snap.SessionID != "" && snap.SessionID != id { return fmt.Errorf("snapshot belongs to workspace %q, not %q", snap.SessionID, id) }
	path := "/workspaces/"+url.PathEscape(id)+"/checkpoints/"+url.PathEscape(snap.ID)+"/restore"
	resp, err := p.request(ctx, http.MethodPost, path, map[string]any{})
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return p.apiError(resp) }
	return nil
}

func (p *NulangCloudProvider) GetStatus(ctx context.Context, id string) (*SessionStatus, error) {
	resp, err := p.request(ctx, http.MethodGet, "/workspaces/"+url.PathEscape(id), nil)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, p.apiError(resp) }
	var out struct { ID string `json:"id"`; Status string `json:"status"`; GuestReady bool `json:"guest_ready"` }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	status := out.Status; if out.GuestReady && status == "running" { status = "ready" }
	return &SessionStatus{SessionID:out.ID, Status:status, LastActive:time.Now().UTC()}, nil
}

func (p *NulangCloudProvider) StreamLogs(context.Context, string) (<-chan LogLine, error) {
	return nil, fmt.Errorf("nulang-cloud log streaming: %w", ErrNotImplemented)
}


const (
	nulangCloudSingleFileBytes = 8 * 1024 * 1024
	nulangCloudChunkBytes = 4 * 1024 * 1024
	nulangCloudStreamFileBytes int64 = 64 * 1024 * 1024 * 1024
)

func cloneRepositoryForSeed(ctx context.Context, req CreateRequest) (string, func(), error) {
	dir, err := os.MkdirTemp("", "devplane-nulang-seed-*")
	if err != nil { return "", nil, err }
	cleanup := func() { _ = os.RemoveAll(dir) }
	repoDir := filepath.Join(dir, "repo")
	args := []string{"clone", "--no-checkout", req.CloneURL, repoDir}
	if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("git clone: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if req.BaseBranch != "" {
		cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", req.BaseBranch)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, fmt.Errorf("git checkout base branch %q: %w: %s", req.BaseBranch, err, strings.TrimSpace(string(out)))
		}
	} else {
		cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", "HEAD")
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, fmt.Errorf("git checkout HEAD: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if req.Branch != "" {
		base := "HEAD"
		if req.BaseBranch != "" { base = req.BaseBranch }
		cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", "-B", req.Branch, base)
		if out, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, fmt.Errorf("git create workspace branch %q from %q: %w: %s", req.Branch, base, err, strings.TrimSpace(string(out)))
		}
	}
	return repoDir, cleanup, nil
}

func archiveRepository(ctx context.Context, repoDir string) (string, func(), error) {
	f, err := os.CreateTemp("", "devplane-nulang-seed-*.tar")
	if err != nil { return "", nil, err }
	path := f.Name()
	if err := f.Close(); err != nil { _ = os.Remove(path); return "", nil, err }
	cmd := exec.CommandContext(ctx, "tar", "-C", repoDir, "-cf", path, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(path)
		return "", nil, fmt.Errorf("archive repository: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return path, func(){ _ = os.Remove(path) }, nil
}

func (p *NulangCloudProvider) writeFileChunk(ctx context.Context, id, path string, offset int64, data []byte, truncate, sync bool) error {
	resp, err := p.request(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(id)+"/files/write-chunk", map[string]any{
		"path": path, "offset": offset, "content_base64": base64.StdEncoding.EncodeToString(data), "truncate": truncate, "sync": sync,
	})
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return p.apiError(resp) }
	return nil
}

func (p *NulangCloudProvider) uploadFileChunks(ctx context.Context, id, path string, reader io.Reader) error {
	buf := make([]byte, nulangCloudChunkBytes)
	var offset int64
	first := true
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if offset > nulangCloudStreamFileBytes-int64(n) {
				return fmt.Errorf("file exceeds Nulang Cloud streamed-file limit of %d bytes", nulangCloudStreamFileBytes)
			}
			if err := p.writeFileChunk(ctx, id, path, offset, buf[:n], first, false); err != nil { return err }
			first = false
			offset += int64(n)
		}
		if readErr == io.EOF { break }
		if readErr != nil { return readErr }
	}
	// A Read call may return a full final chunk with a nil error. Finalize with
	// an empty write at the exact end offset so durability does not depend on
	// detecting EOF on the last data-bearing request.
	return p.writeFileChunk(ctx, id, path, offset, nil, first, true)
}

func (p *NulangCloudProvider) seedRepository(ctx context.Context, id string, req CreateRequest) error {
	repoDir, cleanupRepo, err := p.cloneRepository(ctx, req)
	if err != nil { return err }
	if cleanupRepo != nil { defer cleanupRepo() }

	archive, cleanup, err := archiveRepository(ctx, repoDir)
	if err != nil { return err }
	defer cleanup()

	f, err := os.Open(archive)
	if err != nil { return err }
	defer f.Close()
	if err := p.uploadFileChunks(ctx, id, ".devplane/repo.tar", f); err != nil { return err }
	result, err := p.executeInternalCommand(ctx, id, Command{Args: []string{"tar", "-xf", ".devplane/repo.tar", "-C", "/workspace"}, Dir: "/workspace"})
	if err != nil { return err }
	if result.ExitCode != 0 { return fmt.Errorf("extract repository: %s", strings.TrimSpace(result.Stderr)) }
	result, err = p.executeInternalCommand(ctx, id, Command{Args: []string{"rm", "-f", ".devplane/repo.tar"}, Dir: "/workspace"})
	if err != nil { return err }
	if result.ExitCode != 0 { return fmt.Errorf("remove seed archive: %s", strings.TrimSpace(result.Stderr)) }
	return nil
}
