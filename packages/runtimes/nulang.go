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
	ID            string `json:"id"`
	WorkspaceID   string `json:"workspace_id"`
	CreatedAtMS   int64  `json:"created_at_ms"`
	FormatVersion int    `json:"format_version"`
	RootfsSize    uint64 `json:"rootfs_size_bytes"`
	WorkspaceSize uint64 `json:"workspace_size_bytes"`
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
