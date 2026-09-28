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
}

func NewNulangCloudProvider(baseURL, token string) *NulangCloudProvider {
	return &NulangCloudProvider{
		baseURL: strings.TrimRight(baseURL, "/"),
		token: token,
		client: &http.Client{Timeout: 120 * time.Second},
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
	// Repository acquisition is intentionally outside the no-egress guest. Until
	// trusted archive seeding lands, refuse requests that would imply guest Git.
	if req.CloneURL != "" {
		return nil, fmt.Errorf("nulang-cloud repository seeding is not implemented: trusted archive seeding is required")
	}
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
	return &Session{ID: out.Workspace.WorkspaceID, WorkspaceID: out.Workspace.WorkspaceID, Status: status, Provider: "nulang-cloud", CreatedAt: time.Now().UTC()}, nil
}

func (p *NulangCloudProvider) DestroyWorkspace(ctx context.Context, id string) error {
	resp, err := p.request(ctx, http.MethodDelete, "/workspaces/"+url.PathEscape(id), nil)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return p.apiError(resp) }
	return nil
}

func (p *NulangCloudProvider) ExecuteCommand(ctx context.Context, id string, cmd Command) (*CommandResult, error) {
	command := cmd.Command
	args := cmd.Args
	if len(args) > 0 {
		command, args = args[0], args[1:]
	} else if command == "" {
		return nil, fmt.Errorf("command is required")
	}
	timeoutMS := int64(0)
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
	resp, err := p.request(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(id)+"/files/read", map[string]any{"path": path})
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, p.apiError(resp) }
	// content_base64 needs an explicit tag because the wire contract is snake_case.
	var wire struct { Content string `json:"content_base64"` }
	if err := json.NewDecoder(resp.Body).Decode(&wire); err != nil { return nil, err }
	return base64.StdEncoding.DecodeString(wire.Content)
}

func (p *NulangCloudProvider) WriteFile(ctx context.Context, id, path string, data []byte) error {
	resp, err := p.request(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(id)+"/files/write", map[string]any{"path": path, "content_base64": base64.StdEncoding.EncodeToString(data), "create_parents": true})
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return p.apiError(resp) }
	return nil
}

func (p *NulangCloudProvider) ApplyPatch(ctx context.Context, id, patch string) error {
	const patchPath = ".devplane/patch.diff"
	if err := p.WriteFile(ctx, id, patchPath, []byte(patch)); err != nil { return err }
	result, err := p.ExecuteCommand(ctx, id, Command{Args: []string{"git", "apply", "--whitespace=nowarn", patchPath}, Dir: "/workspace"})
	if err != nil { return err }
	if result.ExitCode != 0 { return fmt.Errorf("git apply failed: %s", strings.TrimSpace(result.Stderr)) }
	_, _ = p.ExecuteCommand(ctx, id, Command{Args: []string{"rm", "-f", patchPath}, Dir: "/workspace"})
	return nil
}

func (p *NulangCloudProvider) Snapshot(ctx context.Context, id string) (*Snapshot, error) {
	resp, err := p.request(ctx, http.MethodPost, "/workspaces/"+url.PathEscape(id)+"/checkpoints", map[string]any{})
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { return nil, p.apiError(resp) }
	var out struct { Checkpoint struct { ID, WorkspaceID string; CreatedAtMS int64 `json:"created_at_ms"` } `json:"checkpoint"` }
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
	var out struct { ID, Status string; GuestReady bool `json:"guest_ready"`; Memory int64 `json:"memory_mb"` }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	status := out.Status; if out.GuestReady && status == "running" { status = "ready" }
	return &SessionStatus{SessionID:out.ID, Status:status, MemoryUsage:out.Memory<<20, LastActive:time.Now().UTC()}, nil
}

func (p *NulangCloudProvider) StreamLogs(context.Context, string) (<-chan LogLine, error) {
	return nil, fmt.Errorf("nulang-cloud log streaming: %w", ErrNotImplemented)
}
