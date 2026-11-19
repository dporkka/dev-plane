package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
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

// Verify the actual worker subscription contract with a disposable NATS
// JetStream server: callback failure must NOT auto-ack the event and must be
// redelivered before the successful handler explicitly acknowledges it.
func TestFailedRunSubscriptionRedeliversAfterTransientFailure(t *testing.T) {
	binary, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skip("nats-server not available for live JetStream redelivery test")
	}
	portReservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portReservation.Addr().(*net.TCPAddr).Port
	portReservation.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := exec.CommandContext(ctx, binary, "-js", "-a", "127.0.0.1", "-p", fmt.Sprint(port), "-sd", filepath.Join(t.TempDir(), "jetstream"))
	if err := server.Start(); err != nil {
		t.Fatalf("start nats-server: %v", err)
	}
	defer func() { cancel(); _ = server.Wait() }()

	url := fmt.Sprintf("nats://127.0.0.1:%d", port)
	var bus *events.Bus
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		bus, err = events.New(url)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect disposable NATS server: %v", err)
	}
	defer bus.Close()
	if err := bus.CreateStreams(); err != nil {
		t.Fatalf("create JetStream streams: %v", err)
	}

	processor := &failOnceRunProcessor{attempts: make(chan uint64, 4)}
	sub, err := bus.SubscribeWithOptions(events.AgentRunFailed, func(msg *nats.Msg) {
		handleRunFailedDelivery(msg, processor, slog.Default())
	}, nats.ManualAck())
	if err != nil {
		t.Fatalf("subscribe failed-run with manual ack: %v", err)
	}
	defer sub.Unsubscribe()

	if err := bus.Publish(events.AgentRunFailed, []byte(`{"run_id":"run-1","task_id":"task-1","status":"failed"}`)); err != nil {
		t.Fatalf("publish run failed: %v", err)
	}
	for want := uint64(1); want <= 2; want++ {
		select {
		case got := <-processor.attempts:
			if got != want {
				t.Fatalf("delivery = %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for JetStream delivery %d", want)
		}
	}
	if got := processor.calls.Load(); got != 2 {
		t.Fatalf("attempts = %d, want exactly 2", got)
	}
}

type failOnceRunProcessor struct {
	calls    atomic.Uint64
	attempts chan uint64
}

func (p *failOnceRunProcessor) HandleRunFailed(msg *nats.Msg) error {
	attempt := p.calls.Add(1)
	p.attempts <- attempt
	if attempt == 1 {
		return errors.New("simulated transient downstream publication failure")
	}
	return msg.Ack()
}
