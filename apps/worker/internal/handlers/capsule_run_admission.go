package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/ai-dev-control-plane/scheduler"
)

type admittedTaskCapsuleBuilder interface {
	BuildAdmittedTaskCapsule(
		context.Context,
		string,
		string,
		[]string,
	) (scheduler.TaskCapsule, error)
}

// CapsulePersistingRunAdmission decorates scheduler admission with the durable
// task-capsule write required before execution can begin.
type CapsulePersistingRunAdmission struct {
	admission        RunAdmission
	builder          admittedTaskCapsuleBuilder
	store            taskCapsuleStore
	requiredEvidence []string
}

func NewCapsulePersistingRunAdmission(
	admission RunAdmission,
	builder admittedTaskCapsuleBuilder,
	store taskCapsuleStore,
	requiredEvidence []string,
) *CapsulePersistingRunAdmission {
	return &CapsulePersistingRunAdmission{
		admission:        admission,
		builder:          builder,
		store:            store,
		requiredEvidence: append([]string(nil), requiredEvidence...),
	}
}

func (a *CapsulePersistingRunAdmission) AdmitRun(
	ctx context.Context,
	runID string,
	taskID string,
) (RunAdmissionDecision, error) {
	if a == nil || a.admission == nil {
		return RunAdmissionDecision{}, errors.New("run admission is required")
	}
	if a.builder == nil {
		return RunAdmissionDecision{}, errors.New("admitted task capsule builder is required")
	}
	if a.store == nil {
		return RunAdmissionDecision{}, errors.New("task capsule store is required")
	}

	decision, err := a.admission.AdmitRun(ctx, runID, taskID)
	if err != nil || !decision.Allowed {
		return decision, err
	}

	capsule, err := a.builder.BuildAdmittedTaskCapsule(ctx, runID, taskID, a.requiredEvidence)
	if err != nil {
		return RunAdmissionDecision{}, a.releaseAfterFailure(
			ctx,
			runID,
			fmt.Errorf("build admitted task capsule: %w", err),
		)
	}
	if err := persistTaskCapsule(ctx, a.store, runID, capsule); err != nil {
		return RunAdmissionDecision{}, a.releaseAfterFailure(
			ctx,
			runID,
			fmt.Errorf("persist admitted task capsule: %w", err),
		)
	}
	return decision, nil
}

func (a *CapsulePersistingRunAdmission) ReleaseRun(ctx context.Context, runID string) error {
	if a == nil || a.admission == nil {
		return errors.New("run admission is required")
	}
	return a.admission.ReleaseRun(ctx, runID)
}

func (a *CapsulePersistingRunAdmission) releaseAfterFailure(
	ctx context.Context,
	runID string,
	cause error,
) error {
	if releaseErr := a.admission.ReleaseRun(ctx, runID); releaseErr != nil {
		return errors.Join(cause, fmt.Errorf("release run admission after capsule failure: %w", releaseErr))
	}
	return cause
}
