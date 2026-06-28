package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestInitRuntimeProvider_RejectsLocalInProduction(t *testing.T) {
	os.Setenv("APP_ENV", "production")
	defer os.Unsetenv("APP_ENV")

	_, _, err := initRuntimeProvider("local", "/tmp/workspaces", "", "")
	if err == nil {
		t.Fatal("expected error for local runtime in production")
	}
}

func TestInitRuntimeProvider_AllowsLocalInDevelopment(t *testing.T) {
	os.Unsetenv("APP_ENV")
	os.Unsetenv("ENV")

	_, _, err := initRuntimeProvider("local", "/tmp/workspaces", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnvOrDefault(t *testing.T) {
	key := "TEST_ENV_OR_DEFAULT"
	os.Unsetenv(key)
	defer os.Unsetenv(key)

	if got := envOrDefault(key, "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}

	os.Setenv(key, "value")
	if got := envOrDefault(key, "fallback"); got != "value" {
		t.Fatalf("expected value, got %q", got)
	}
}

func TestStartHealthServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	addr, stop := startHealthServer(ctx, "0", nil)
	defer stop()

	if addr == "" {
		t.Fatal("expected health server address")
	}

	url := fmt.Sprintf("http://%s/health", addr)

	var resp *http.Response
	var err error
	for i := 0; i < 20; i++ {
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("health server unreachable: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != `{"status":"healthy"}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestStartHealthServer_Shutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	addr, stop := startHealthServer(ctx, "0", nil)
	if addr == "" {
		t.Fatal("expected health server address")
	}

	cancel()
	stop()

	// After shutdown the server should not be reachable.
	client := &http.Client{Timeout: 1 * time.Second}
	url := fmt.Sprintf("http://%s/health", addr)
	_, err := client.Get(url)
	if err == nil {
		t.Fatal("expected error after shutdown")
	}
}
