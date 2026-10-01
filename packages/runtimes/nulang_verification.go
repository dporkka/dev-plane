package runtimes

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultNulangVerificationPollInterval = 250 * time.Millisecond
	maxNulangDurableLogChunkBytes         = 64 * 1024
	nulangCancelTimeout                   = 5 * time.Second
)

// NulangVerificationProvider is the verifier-facing Nulang runtime adapter.
// It preserves unary execution for short commands and uses Nulang Cloud's
// durable execution jobs for commands whose timeout exceeds the unary RPC
// ceiling. Tenant authority is always carried explicitly on internal requests.
type NulangVerificationProvider struct {
	*NulangCloudProvider
	tenant       string
	pollInterval time.Duration
}

// NewNulangVerificationProvider wraps an existing Nulang provider with the
// verifier-specific tenant and durable-job semantics. The wrapper mutates the
// provider's HTTP client transport once so all existing unary/file operations
// also carry the same trusted tenant context.
func NewNulangVerificationProvider(base *NulangCloudProvider, tenant string) (*NulangVerificationProvider, error) {
	if base == nil || base.RemoteProvider == nil {
		return nil, errors.New("Nulang verification provider requires a base provider")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return nil, errors.New("Nulang verification provider requires an explicit tenant")
	}
	if base.RemoteProvider.client == nil {
		return nil, errors.New("Nulang verification provider requires an HTTP client")
	}

	clone := *base.RemoteProvider.client
	transport := clone.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clone.Transport = &nulangTenantTransport{base: transport, tenant: tenant}
	base.RemoteProvider.client = &clone

	return &NulangVerificationProvider{
		NulangCloudProvider: base,
		tenant:               tenant,
		pollInterval:         defaultNulangVerificationPollInterval,
	}, nil
}

type nulangTenantTransport struct {
	base   http.RoundTripper
	tenant string
}

func (t *nulangTenantTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	clone.Header.Set("X-NLC-Tenant", t.tenant)
	return t.base.RoundTrip(clone)
}

type nulangExecJobState string

const (
	nulangExecJobQueued      nulangExecJobState = "queued"
	nulangExecJobRunning     nulangExecJobState = "running"
	nulangExecJobSucceeded   nulangExecJobState = "succeeded"
	nulangExecJobFailed      nulangExecJobState = "failed"
	nulangExecJobCancelled   nulangExecJobState = "cancelled"
	nulangExecJobTimedOut    nulangExecJobState = "timed_out"
	nulangExecJobInterrupted nulangExecJobState = "interrupted"
)

type nulangStartExecJobRequest struct {
	RequestID      string            `json:"request_id"`
	Args           []string          `json:"args"`
	Dir            string            `json:"dir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutSeconds int64             `json:"timeout_seconds"`
}

type nulangExecJob struct {
	JobID         string             `json:"job_id"`
	RequestID     string             `json:"request_id"`
	State         nulangExecJobState `json:"state"`
	Stdout        string             `json:"stdout"`
	Stderr        string             `json:"stderr"`
	ExitCode      *int               `json:"exit_code,omitempty"`
	DurationMS    int64              `json:"duration_ms"`
	LogsTruncated bool               `json:"logs_truncated"`
}

type nulangExecJobLogs struct {
	JobID             string             `json:"job_id"`
	State             nulangExecJobState `json:"state"`
	StdoutBase64      string             `json:"stdout_base64"`
	StderrBase64      string             `json:"stderr_base64"`
	NextStdoutOffset  uint64             `json:"next_stdout_offset"`
	NextStderrOffset  uint64             `json:"next_stderr_offset"`
	Complete          bool               `json:"complete"`
	Truncated         bool               `json:"truncated"`
}

type nulangCancelExecJobRequest struct {
	RequestID string `json:"request_id"`
}

// ExecuteCommand routes short verifier commands through the existing unary
// RPC and long verifier commands through durable start/log/status/cancel.
func (p *NulangVerificationProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd Command) (*CommandResult, error) {
	timeout := cmd.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout <= nulangMaxExecTimeout {
		return p.NulangCloudProvider.ExecuteCommand(ctx, sessionID, cmd)
	}
	return p.executeDurableCommand(ctx, sessionID, cmd, timeout)
}

func (p *NulangVerificationProvider) executeDurableCommand(ctx context.Context, sessionID string, cmd Command, timeout time.Duration) (*CommandResult, error) {
	command, args, err := normalizeNulangCommand(cmd)
	if err != nil {
		return nil, err
	}
	argv := append([]string{command}, args...)
	requestID, err := durableVerificationRequestID(sessionID, argv, cmd.Dir, cmd.Env, timeout)
	if err != nil {
		return nil, err
	}

	start := nulangStartExecJobRequest{
		RequestID:      requestID,
		Args:           argv,
		Dir:            cmd.Dir,
		Env:            cmd.Env,
		TimeoutSeconds: int64((timeout + time.Second - 1) / time.Second),
	}
	var job nulangExecJob
	path := "/workspaces/" + url.PathEscape(sessionID) + "/exec/jobs"
	if err := p.doJSON(ctx, http.MethodPost, path, start, &job, requestID); err != nil {
		return nil, fmt.Errorf("start durable Nulang exec: %w", err)
	}
	if strings.TrimSpace(job.JobID) == "" {
		return nil, errors.New("start durable Nulang exec: empty job id")
	}

	var stdout, stderr strings.Builder
	var stdoutOffset, stderrOffset uint64
	for {
		if err := ctx.Err(); err != nil {
			p.cancelDurableJob(sessionID, job.JobID, requestID)
			return nil, err
		}

		logsPath := fmt.Sprintf(
			"/workspaces/%s/exec/jobs/%s/logs?stdout_offset=%d&stderr_offset=%d&max_bytes=%d",
			url.PathEscape(sessionID), url.PathEscape(job.JobID), stdoutOffset, stderrOffset, maxNulangDurableLogChunkBytes,
		)
		var logs nulangExecJobLogs
		if err := p.doJSON(ctx, http.MethodGet, logsPath, nil, &logs, ""); err != nil {
			if ctx.Err() != nil {
				p.cancelDurableJob(sessionID, job.JobID, requestID)
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("read durable Nulang exec logs: %w", err)
		}
		stdoutChunk, err := base64.StdEncoding.DecodeString(logs.StdoutBase64)
		if err != nil {
			return nil, fmt.Errorf("decode durable Nulang stdout: %w", err)
		}
		stderrChunk, err := base64.StdEncoding.DecodeString(logs.StderrBase64)
		if err != nil {
			return nil, fmt.Errorf("decode durable Nulang stderr: %w", err)
		}
		stdout.Write(stdoutChunk)
		stderr.Write(stderrChunk)
		if logs.NextStdoutOffset < stdoutOffset || logs.NextStderrOffset < stderrOffset {
			return nil, errors.New("durable Nulang log offsets moved backwards")
		}
		stdoutOffset = logs.NextStdoutOffset
		stderrOffset = logs.NextStderrOffset

		if logs.Complete {
			break
		}
		interval := p.pollInterval
		if interval <= 0 {
			interval = defaultNulangVerificationPollInterval
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			p.cancelDurableJob(sessionID, job.JobID, requestID)
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	statusPath := "/workspaces/" + url.PathEscape(sessionID) + "/exec/jobs/" + url.PathEscape(job.JobID)
	if err := p.doJSON(ctx, http.MethodGet, statusPath, nil, &job, ""); err != nil {
		return nil, fmt.Errorf("read durable Nulang exec status: %w", err)
	}
	result := &CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: -1,
		Duration: time.Duration(job.DurationMS) * time.Millisecond,
	}
	if job.ExitCode != nil {
		result.ExitCode = *job.ExitCode
	}

	switch job.State {
	case nulangExecJobSucceeded, nulangExecJobFailed:
		return result, nil
	case nulangExecJobTimedOut:
		return nil, fmt.Errorf("%w after %s", ErrCommandTimeout, timeout)
	case nulangExecJobCancelled:
		return nil, errors.New("durable Nulang exec was cancelled")
	case nulangExecJobInterrupted:
		return nil, errors.New("durable Nulang exec was interrupted by runtime restart")
	default:
		return nil, fmt.Errorf("durable Nulang exec completed logs in non-terminal state %q", job.State)
	}
}

func (p *NulangVerificationProvider) cancelDurableJob(sessionID, jobID, startRequestID string) {
	ctx, cancel := context.WithTimeout(context.Background(), nulangCancelTimeout)
	defer cancel()
	requestID := "cancel:" + startRequestID
	path := "/workspaces/" + url.PathEscape(sessionID) + "/exec/jobs/" + url.PathEscape(jobID) + "/cancel"
	var ignored nulangExecJob
	_ = p.doJSON(ctx, http.MethodPost, path, nulangCancelExecJobRequest{RequestID: requestID}, &ignored, requestID)
}

func durableVerificationRequestID(sessionID string, argv []string, dir string, env map[string]string, timeout time.Duration) (string, error) {
	payload := struct {
		SessionID string            `json:"session_id"`
		Args      []string          `json:"args"`
		Dir       string            `json:"dir"`
		Env       map[string]string `json:"env"`
		TimeoutNS int64             `json:"timeout_ns"`
	}{
		SessionID: sessionID,
		Args:      argv,
		Dir:       dir,
		Env:       env,
		TimeoutNS: timeout.Nanoseconds(),
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode durable verification identity: %w", err)
	}
	digest := sha256.Sum256(bytes)
	return "verify:" + hex.EncodeToString(digest[:16]), nil
}

var _ Provider = (*NulangVerificationProvider)(nil)
