package handlers

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
)

type recordingCompletionVerifier struct {
	calls   int
	runID   string
	taskID  string
	receipt CompletionVerification
	err     error
}

func (v *recordingCompletionVerifier) VerifyCompletedRun(_ context.Context, runID, taskID string) (CompletionVerification, error) {
	v.calls++
	v.runID, v.taskID = runID, taskID
	return v.receipt, v.err
}

func TestRunCompletionRequiresVerifiedExactHeadBeforeReview(t *testing.T) {
	base := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	for _, tc := range []struct {
		name       string
		verifyErr  error
		receipt    CompletionVerification
		reviewBase string
		reviewHead string
		success    bool
	}{
		{name: "matching verified review", receipt: CompletionVerification{BaseSHA: base, HeadSHA: head}, reviewBase: base, reviewHead: head, success: true},
		{name: "verifier fails", verifyErr: errors.New("required verification gate failed"), reviewBase: base, reviewHead: head},
		{name: "missing verified head", receipt: CompletionVerification{BaseSHA: base}, reviewBase: base, reviewHead: head},
		{name: "review base differs", receipt: CompletionVerification{BaseSHA: base, HeadSHA: head}, reviewBase: head, reviewHead: head},
		{name: "review head differs", receipt: CompletionVerification{BaseSHA: base, HeadSHA: head}, reviewBase: base, reviewHead: base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupRunHandlerDB(t)
			defer db.Close()
			insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
			verify := &recordingCompletionVerifier{err: tc.verifyErr, receipt: tc.receipt}
			review := &fakeReviewer{report: &reviewer.ReviewReport{
				RunID: "run-1", RiskLevel: "low", Approvable: true,
				DiffSummary: reviewer.DiffSummary{BaseSHA: tc.reviewBase, HeadSHA: tc.reviewHead},
			}}
			h := NewRunHandler(db, slog.Default(), nil).
				WithCompletionVerifier(verify).
				WithReviewer(review)
			err := h.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)})
			if (err == nil) != tc.success {
				t.Fatalf("HandleRunCompleted error = %v, success=%v", err, tc.success)
			}
			if verify.calls != 1 || verify.runID != "run-1" || verify.taskID != "task-1" {
				t.Fatalf("verifier called %d times for run %s task %s", verify.calls, verify.runID, verify.taskID)
			}
			var status string
			if err := db.QueryRow("SELECT status FROM tasks WHERE id='task-1'").Scan(&status); err != nil {
				t.Fatal(err)
			}
			if !tc.success {
				if status == "reviewing" {
					t.Fatalf("failed verification/review advanced task to %q", status)
				}
			} else if status != "reviewing" {
				t.Fatalf("successful verification left status %q", status)
			}
			if !tc.success && (tc.verifyErr != nil || tc.receipt.HeadSHA == "") && review.runID != "" {
				t.Fatalf("review invoked despite missing verification: %q", review.runID)
			}
		})
	}
}

func TestRunCompletionDoesNotVerifyIntermediateMailboxHandoff(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	insertHandoffFixture(t, db, "handoff-1", "run-1", models.AgentRoleReviewer)
	verify := &recordingCompletionVerifier{err: errors.New("should not run")}
	h := NewRunHandler(db, slog.Default(), nil).WithCompletionVerifier(verify)
	if err := h.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)}); err != nil {
		t.Fatalf("mailbox handoff failed: %v", err)
	}
	if verify.calls != 0 {
		t.Fatalf("verified intermediate run %d times", verify.calls)
	}
}

// A passing repository verifier does not authorize publishing a review that
// the independent reviewer explicitly marked non-approvable.
func TestRunCompletionRejectsNonApprovableReview(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	base := strings.Repeat("a", 40)
	head := strings.Repeat("b", 40)
	verified := &recordingCompletionVerifier{receipt: CompletionVerification{BaseSHA: base, HeadSHA: head}}
	review := &fakeReviewer{report: &reviewer.ReviewReport{
		RunID:       "run-1",
		RiskLevel:   "high",
		Approvable:  false,
		DiffSummary: reviewer.DiffSummary{BaseSHA: base, HeadSHA: head},
	}}
	handler := NewRunHandler(db, slog.Default(), nil).WithCompletionVerifier(verified).WithReviewer(review)
	err := handler.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)})
	if err == nil {
		t.Fatal("non-approvable review advanced despite exact-HEAD verification")
	}
	var status string
	if err := db.QueryRow("SELECT status FROM tasks WHERE id='task-1'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status == "reviewing" {
		t.Fatal("non-approvable review advanced the task")
	}
}
