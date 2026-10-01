package handlers

import (
	"context"
	"errors"
	"reflect"
	"testing"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/scheduler"
)

type fakeCapsuleBuilder struct {
	capsule scheduler.TaskCapsule
	err error
	calls int
	runID string
	taskID string
	required []string
}

func (b *fakeCapsuleBuilder) BuildAdmittedTaskCapsule(
	_ context.Context,
	runID string,
	taskID string,
	requiredEvidence []string,
) (scheduler.TaskCapsule, error) {
	b.calls++
	b.runID = runID
	b.taskID = taskID
	b.required = append([]string(nil), requiredEvidence...)
	return b.capsule, b.err
}

type trackingRunAdmission struct {
	decision RunAdmissionDecision
	admitErr error
	releaseErr error
	admitCalls int
	releaseCalls int
	releasedRunID string
}

func (a *trackingRunAdmission) AdmitRun(_ context.Context, _, _ string) (RunAdmissionDecision, error) {
	a.admitCalls++
	return a.decision, a.admitErr
}

func (a *trackingRunAdmission) ReleaseRun(_ context.Context, runID string) error {
	a.releaseCalls++
	a.releasedRunID = runID
	return a.releaseErr
}

func TestCapsulePersistingRunAdmissionPersistsAllowedRunBeforeReturning(t *testing.T) {
	underlying := &trackingRunAdmission{
		decision: RunAdmissionDecision{Allowed: true},
	}
	builder := &fakeCapsuleBuilder{
		capsule: scheduler.TaskCapsule{
			Version: scheduler.TaskCapsuleVersion,
			TaskID: "task-1",
			WorkspaceID: "workspace-1",
			Agent: scheduler.AgentIdentity{ID: "run-1", Role: "implementer"},
			Leases: []scheduler.Lease{{Path: "apps/api", Mode: scheduler.LeaseModeExclusive}},
			RequiredEvidence: []string{"tests", "lint"},
		},
	}
	store := &recordingCapsuleStore{}

	admission := NewCapsulePersistingRunAdmission(
		underlying,
		builder,
		store,
		[]string{"tests", "lint"},
	)

	decision, err := admission.AdmitRun(context.Background(), "run-1", "task-1")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("decision = %#v, want allowed", decision)
	}
	if builder.calls != 1 || builder.runID != "run-1" || builder.taskID != "task-1" {
		t.Fatalf("builder state = calls=%d run=%q task=%q", builder.calls, builder.runID, builder.taskID)
	}
	if !reflect.DeepEqual(builder.required, []string{"tests", "lint"}) {
		t.Fatalf("required evidence = %#v", builder.required)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
	if store.record.AgentRunID != "run-1" || store.record.TaskID != "task-1" {
		t.Fatalf("persisted record = %+v", store.record)
	}
	if !reflect.DeepEqual(store.leases, []dbpkg.TaskLeaseRecord{{Path: "apps/api", Mode: "exclusive"}}) {
		t.Fatalf("persisted leases = %#v", store.leases)
	}
}

func TestCapsulePersistingRunAdmissionDoesNotPersistDeniedRun(t *testing.T) {
	underlying := &trackingRunAdmission{
		decision: RunAdmissionDecision{Allowed: false, Reason: "resource-capacity"},
	}
	builder := &fakeCapsuleBuilder{}
	store := &recordingCapsuleStore{}

	admission := NewCapsulePersistingRunAdmission(underlying, builder, store, []string{"tests"})
	decision, err := admission.AdmitRun(context.Background(), "run-1", "task-1")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed || decision.Reason != "resource-capacity" {
		t.Fatalf("decision = %#v", decision)
	}
	if builder.calls != 0 || store.calls != 0 {
		t.Fatalf("builder/store calls = %d/%d, want 0/0", builder.calls, store.calls)
	}
}

func TestCapsulePersistingRunAdmissionReleasesClaimWhenBuildFails(t *testing.T) {
	buildErr := errors.New("build capsule failed")
	underlying := &trackingRunAdmission{
		decision: RunAdmissionDecision{Allowed: true},
	}
	builder := &fakeCapsuleBuilder{err: buildErr}
	store := &recordingCapsuleStore{}

	admission := NewCapsulePersistingRunAdmission(underlying, builder, store, []string{"tests"})
	_, err := admission.AdmitRun(context.Background(), "run-1", "task-1")
	if err == nil || !errors.Is(err, buildErr) {
		t.Fatalf("expected wrapped build failure, got %v", err)
	}
	if underlying.releaseCalls != 1 || underlying.releasedRunID != "run-1" {
		t.Fatalf("release state = calls=%d run=%q", underlying.releaseCalls, underlying.releasedRunID)
	}
	if store.calls != 0 {
		t.Fatalf("store calls = %d, want 0", store.calls)
	}
}

func TestCapsulePersistingRunAdmissionReleasesClaimWhenPersistenceFails(t *testing.T) {
	writeErr := errors.New("write failed")
	underlying := &trackingRunAdmission{
		decision: RunAdmissionDecision{Allowed: true},
	}
	builder := &fakeCapsuleBuilder{
		capsule: scheduler.TaskCapsule{
			Version: scheduler.TaskCapsuleVersion,
			TaskID: "task-1",
			WorkspaceID: "workspace-1",
			Agent: scheduler.AgentIdentity{ID: "run-1", Role: "implementer"},
		},
	}
	store := &recordingCapsuleStore{err: writeErr}

	admission := NewCapsulePersistingRunAdmission(underlying, builder, store, nil)
	_, err := admission.AdmitRun(context.Background(), "run-1", "task-1")
	if err == nil || !errors.Is(err, writeErr) {
		t.Fatalf("expected wrapped persistence failure, got %v", err)
	}
	if underlying.releaseCalls != 1 || underlying.releasedRunID != "run-1" {
		t.Fatalf("release state = calls=%d run=%q", underlying.releaseCalls, underlying.releasedRunID)
	}
}

func TestCapsulePersistingRunAdmissionReleaseDelegates(t *testing.T) {
	underlying := &trackingRunAdmission{}
	builder := &fakeCapsuleBuilder{}
	store := &recordingCapsuleStore{}

	admission := NewCapsulePersistingRunAdmission(underlying, builder, store, nil)
	if err := admission.ReleaseRun(context.Background(), "run-1"); err != nil {
		t.Fatalf("ReleaseRun() error: %v", err)
	}
	if underlying.releaseCalls != 1 || underlying.releasedRunID != "run-1" {
		t.Fatalf("release state = calls=%d run=%q", underlying.releaseCalls, underlying.releasedRunID)
	}
}
