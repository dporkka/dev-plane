package handlers

import (
	"context"
	"log/slog"
	"testing"

	"github.com/nats-io/nats.go"
)

type suspendedRunTestError struct{}

func (suspendedRunTestError) Error() string       { return "external agent waiting for approval" }
func (suspendedRunTestError) RunSuspended() bool  { return true }

type suspendedRunExecutor struct {
	runID string
}

func (e *suspendedRunExecutor) ExecuteRun(_ context.Context, runID string) error {
	e.runID = runID
	return suspendedRunTestError{}
}

type suspensionAdmission struct {
	admittedRun string
	releasedRun string
}

func (a *suspensionAdmission) AdmitRun(_ context.Context, runID, _ string) (RunAdmissionDecision, error) {
	a.admittedRun = runID
	return RunAdmissionDecision{Allowed: true}, nil
}

func (a *suspensionAdmission) ReleaseRun(_ context.Context, runID string) error {
	a.releasedRun = runID
	return nil
}

func TestHandleRunTriggeredTreatsDurableSuspensionAsSuccessfulDispatch(t *testing.T) {
	executor := &suspendedRunExecutor{}
	admission := &suspensionAdmission{}
	handler := NewRunHandler(nil, slog.Default(), nil).
		WithRunExecutor(executor).
		WithRunAdmission(admission)

	msg := &nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1","status":"queued"}`)}
	if err := handler.HandleRunTriggered(msg); err != nil {
		t.Fatalf("HandleRunTriggered() error = %v, want nil suspension handoff", err)
	}
	if executor.runID != "run-1" {
		t.Fatalf("executor run id = %q", executor.runID)
	}
	if admission.admittedRun != "run-1" || admission.releasedRun != "run-1" {
		t.Fatalf("admission = admitted:%q released:%q", admission.admittedRun, admission.releasedRun)
	}
}

func TestRunSuspensionDetectionRejectsOrdinaryErrors(t *testing.T) {
	if isRunSuspension(context.DeadlineExceeded) {
		t.Fatal("deadline error must not be treated as durable suspension")
	}
	if !isRunSuspension(suspendedRunTestError{}) {
		t.Fatal("suspension error was not detected")
	}
}
