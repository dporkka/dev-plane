package runtimes

import (
	"testing"
	"time"
)

func assertEqual(t *testing.T, got, want interface{}) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func assertTrue(t *testing.T, got bool, msg string) {
	t.Helper()
	if !got {
		t.Errorf("expected true: %s", msg)
	}
}

// Compile-time check that LocalProvider implements Provider.
// This is verified by the assignment in local.go, but we confirm it here too.
var _ Provider = (*LocalProvider)(nil)

func TestProviderInterface(t *testing.T) {
	// This test exists to ensure the compile-time interface check is exercised.
	// If LocalProvider does not implement Provider, this file will not compile.
	assertTrue(t, true, "LocalProvider implements Provider interface")
}

func TestSession_StatusValues(t *testing.T) {
	s := &Session{
		ID:          "sess-1",
		WorkspaceID: "ws-1",
		Status:      "ready",
		Provider:    "local",
		CreatedAt:   time.Now(),
	}
	assertEqual(t, s.Status, "ready")
	assertEqual(t, s.ID, "sess-1")
	assertEqual(t, s.WorkspaceID, "ws-1")
	assertEqual(t, s.Provider, "local")
}

func TestCommandResult_Values(t *testing.T) {
	cr := &CommandResult{
		Stdout:   "hello world",
		Stderr:   "",
		ExitCode: 0,
		Duration: 1500 * time.Millisecond,
	}
	assertEqual(t, cr.Stdout, "hello world")
	assertEqual(t, cr.Stderr, "")
	assertEqual(t, cr.ExitCode, 0)
	assertEqual(t, cr.Duration, 1500*time.Millisecond)
}

func TestCommandResult_FailureValues(t *testing.T) {
	cr := &CommandResult{
		Stdout:   "",
		Stderr:   "error: something went wrong",
		ExitCode: 1,
		Duration: 500 * time.Millisecond,
	}
	assertEqual(t, cr.Stdout, "")
	assertEqual(t, cr.Stderr, "error: something went wrong")
	assertEqual(t, cr.ExitCode, 1)
}

func TestLogLine_Values(t *testing.T) {
	now := time.Now()
	ll := LogLine{
		Timestamp: now,
		Stream:    "stdout",
		Message:   "Build completed successfully",
	}
	assertEqual(t, ll.Stream, "stdout")
	assertEqual(t, ll.Message, "Build completed successfully")
	assertTrue(t, !ll.Timestamp.IsZero(), "timestamp should be set")
}

func TestLogLine_StderrStream(t *testing.T) {
	ll := LogLine{
		Timestamp: time.Now(),
		Stream:    "stderr",
		Message:   "warning: deprecated function",
	}
	assertEqual(t, ll.Stream, "stderr")
	assertEqual(t, ll.Message, "warning: deprecated function")
}
