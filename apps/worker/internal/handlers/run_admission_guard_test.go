package handlers

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"
)

// A missing admission service is not an opt-out. Misconfigured workers must
// not execute arbitrary triggered runs with no scheduler or budget checks.
func TestHandleRunTriggeredMissingAdmissionFailsClosed(t *testing.T) {
	executor := &fakeRunExecutor{}
	handler := NewRunHandler(nil, slog.Default(), nil).WithRunExecutor(executor)
	err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"run-a","task_id":"task-a"}`)})
	if !errors.Is(err, ErrRunAdmissionDeferred) {
		t.Fatalf("missing admission = %v, want deferred denial", err)
	}
	if executor.runID != "" {
		t.Fatalf("executor ran without admission: %q", executor.runID)
	}
}

func TestHandleRunTriggeredTypedNilAdmissionFailsClosed(t *testing.T) {
	executor := &fakeRunExecutor{}
	var admission *SchedulerAdmission
	handler := NewRunHandler(nil, slog.Default(), nil).
		WithRunExecutor(executor).
		WithRunAdmission(admission)
	err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"run-a","task_id":"task-a"}`)})
	if !errors.Is(err, ErrRunAdmissionDeferred) {
		t.Fatalf("nil scheduler = %v, want deferred denial", err)
	}
	if executor.runID != "" {
		t.Fatalf("executor ran with nil scheduler: %q", executor.runID)
	}
}
