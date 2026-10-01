package runtimes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNulangVerificationProviderUsesDurableJobsForLongCommands(t *testing.T) {
	var logReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Auth-Token"); got != "internal-secret" {
			t.Fatalf("X-Internal-Auth-Token = %q", got)
		}
		if got := r.Header.Get("X-NLC-Tenant"); got != "tenant-a" {
			t.Fatalf("X-NLC-Tenant = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/exec/jobs":
			var request struct {
				RequestID      string            `json:"request_id"`
				Args           []string          `json:"args"`
				Dir            string            `json:"dir"`
				Env            map[string]string `json:"env"`
				TimeoutSeconds int               `json:"timeout_seconds"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode start job: %v", err)
			}
			if request.RequestID == "" || strings.Join(request.Args, " ") != "pnpm run quality" {
				t.Fatalf("start request = %#v", request)
			}
			if request.Dir != "app" || request.Env["CI"] != "true" || request.TimeoutSeconds != 120 {
				t.Fatalf("start request = %#v", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"job_id": "job-1", "request_id": request.RequestID, "state": "running",
				"stdout": "", "stderr": "", "duration_ms": 0, "logs_truncated": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces/ws-1/exec/jobs/job-1/logs":
			call := logReads.Add(1)
			stdoutOffset := r.URL.Query().Get("stdout_offset")
			if call == 1 {
				if stdoutOffset != "0" {
					t.Fatalf("first stdout offset = %q", stdoutOffset)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"job_id": "job-1", "state": "running",
					"stdout_base64": base64.StdEncoding.EncodeToString([]byte("first")),
					"stderr_base64": "", "next_stdout_offset": 5, "next_stderr_offset": 0,
					"complete": false, "truncated": false,
				})
				return
			}
			if stdoutOffset != "5" {
				t.Fatalf("second stdout offset = %q", stdoutOffset)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"job_id": "job-1", "state": "succeeded",
				"stdout_base64": base64.StdEncoding.EncodeToString([]byte("second")),
				"stderr_base64": "", "next_stdout_offset": 11, "next_stderr_offset": 0,
				"complete": true, "truncated": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces/ws-1/exec/jobs/job-1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"job_id": "job-1", "request_id": "verify-request", "state": "succeeded",
				"stdout": "firstsecond", "stderr": "", "exit_code": 0,
				"duration_ms": 42, "logs_truncated": false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	provider, err := NewNulangVerificationProvider(base, "tenant-a")
	if err != nil {
		t.Fatalf("NewNulangVerificationProvider() error = %v", err)
	}
	provider.pollInterval = time.Millisecond

	result, err := provider.ExecuteCommand(context.Background(), "ws-1", Command{
		Args:    []string{"pnpm", "run", "quality"},
		Dir:     "app",
		Env:     map[string]string{"CI": "true"},
		Timeout: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("ExecuteCommand() error = %v", err)
	}
	if result.ExitCode != 0 || result.Stdout != "firstsecond" || result.Stderr != "" {
		t.Fatalf("result = %#v", result)
	}
	if logReads.Load() != 2 {
		t.Fatalf("log reads = %d, want 2", logReads.Load())
	}
}

func TestNulangVerificationProviderKeepsShortCommandsOnUnaryExec(t *testing.T) {
	var durableCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-NLC-Tenant"); got != "tenant-a" {
			t.Fatalf("X-NLC-Tenant = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/exec/jobs") {
			durableCalled.Store(true)
			t.Fatal("short command used durable job endpoint")
		}
		if r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/exec" {
			zero := 0
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind": "exec", "exit_code": zero, "stdout_base64": base64.StdEncoding.EncodeToString([]byte("ok\n")),
				"stderr_base64": "", "timed_out": false,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	base := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	provider, err := NewNulangVerificationProvider(base, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.ExecuteCommand(context.Background(), "ws-1", Command{
		Args:    []string{"git", "status", "--short"},
		Timeout: 10 * time.Second,
	})
	if err != nil || result.Stdout != "ok\n" {
		t.Fatalf("ExecuteCommand() = %#v, %v", result, err)
	}
	if durableCalled.Load() {
		t.Fatal("durable endpoint was called")
	}
}

func TestNulangVerificationProviderCancelsDurableJobWhenContextEnds(t *testing.T) {
	started := make(chan struct{}, 1)
	cancelled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/exec/jobs":
			started <- struct{}{}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"job_id": "job-1", "request_id": "req", "state": "running",
				"stdout": "", "stderr": "", "duration_ms": 0, "logs_truncated": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces/ws-1/exec/jobs/job-1/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"job_id": "job-1", "state": "running", "stdout_base64": "", "stderr_base64": "",
				"next_stdout_offset": 0, "next_stderr_offset": 0, "complete": false, "truncated": false,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces/ws-1/exec/jobs/job-1/cancel":
			cancelled <- struct{}{}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"job_id": "job-1", "request_id": "req", "state": "cancelled",
				"stdout": "", "stderr": "cancelled", "duration_ms": 1, "logs_truncated": false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	base := NewNulangCloudProvider(server.URL, "internal-secret").WithHTTPClient(server.Client())
	provider, err := NewNulangVerificationProvider(base, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	provider.pollInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, executeErr := provider.ExecuteCommand(ctx, "ws-1", Command{
			Args: []string{"sleep", "120"}, Timeout: 2 * time.Minute,
		})
		result <- executeErr
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("durable job was not started")
	}
	select {
	case executeErr := <-result:
		if executeErr == nil {
			t.Fatal("expected cancelled context error")
		}
	case <-time.After(time.Second):
		t.Fatal("ExecuteCommand did not return after context cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("durable job cancel endpoint was not called")
	}
}

func TestNulangVerificationProviderRequiresTenant(t *testing.T) {
	base := NewNulangCloudProvider("http://127.0.0.1:1", "token")
	if _, err := NewNulangVerificationProvider(base, "  "); err == nil {
		t.Fatal("expected empty tenant to fail closed")
	}
}
