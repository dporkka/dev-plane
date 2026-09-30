package handlers

import (
	"context"
	"fmt"

	"github.com/ai-dev-control-plane/scheduler"
)

// TaskCapsuleCompletionObserver is the execution-side completion gate. A run is
// not allowed to become completed until its final checks have been persisted
// against the exact workspace revision and satisfy the admitted capsule.
type TaskCapsuleCompletionObserver struct {
	store taskCapsuleEvidenceStore
}

func NewTaskCapsuleCompletionObserver(store taskCapsuleEvidenceStore) *TaskCapsuleCompletionObserver {
	return &TaskCapsuleCompletionObserver{store: store}
}

func (o *TaskCapsuleCompletionObserver) RecordRunCompletion(
	ctx context.Context,
	runID string,
	subjectRevision string,
	evidence []scheduler.Evidence,
) error {
	if o == nil || o.store == nil {
		return fmt.Errorf("task capsule completion store is required")
	}
	if _, err := advanceTaskCapsuleSubjectRevision(ctx, o.store, runID, subjectRevision); err != nil {
		return fmt.Errorf("bind task capsule subject revision: %w", err)
	}
	for _, item := range evidence {
		item.SubjectRevision = subjectRevision
		if _, err := recordTaskCapsuleEvidence(ctx, o.store, runID, item); err != nil {
			return fmt.Errorf("record task capsule evidence %q: %w", item.Name, err)
		}
	}
	if err := verifyTaskCapsuleCompletion(ctx, o.store, runID); err != nil {
		return err
	}
	return nil
}
