package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/events"
	runfailure "github.com/ai-dev-control-plane/failure"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
)

func TestScheduleFollowOnRunConsumesHandoffAndQueuesNextRole(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	insertHandoffFixture(t, db, "message-1", "run-1", models.AgentRoleReviewer)

	handler := NewRunHandler(db, slog.Default(), nil)
	scheduled, nextRunID, nextRole, err := handler.scheduleFollowOnRun(context.Background(), events.AgentRunEvent{
		RunID:  "run-1",
		TaskID: "task-1",
	})
	if err != nil {
		t.Fatalf("scheduleFollowOnRun() error: %v", err)
	}
	if !scheduled {
		t.Fatal("scheduled = false, want true")
	}
	if nextRunID == "" {
		t.Fatal("nextRunID is empty")
	}
	if nextRole != models.AgentRoleReviewer {
		t.Fatalf("nextRole = %q, want reviewer", nextRole)
	}

	var role, status, workspaceID, metadata string
	if err := db.QueryRow(`
		SELECT agent_role, status, workspace_id, metadata
		FROM agent_runs
		WHERE id = ?
	`, nextRunID).Scan(&role, &status, &workspaceID, &metadata); err != nil {
		t.Fatalf("query next run: %v", err)
	}
	if role != models.AgentRoleReviewer || status != "queued" || workspaceID != "workspace-1" {
		t.Fatalf("next run = role %q status %q workspace %q", role, status, workspaceID)
	}
	if !contains(metadata, "message-1") || !contains(metadata, "mailbox_handoff") || !contains(metadata, "task-readiness-v1") {
		t.Fatalf("metadata = %s, want handoff trace and inherited admission evidence", metadata)
	}

	var consumedBy sql.NullString
	var consumedAt sql.NullString
	if err := db.QueryRow(`
		SELECT consumed_by_run_id, consumed_at
		FROM agent_messages
		WHERE id = 'message-1'
	`).Scan(&consumedBy, &consumedAt); err != nil {
		t.Fatalf("query consumed handoff: %v", err)
	}
	if !consumedBy.Valid || consumedBy.String != nextRunID {
		t.Fatalf("consumed_by_run_id = %v, want %s", consumedBy, nextRunID)
	}
	if !consumedAt.Valid || consumedAt.String == "" {
		t.Fatalf("consumed_at = %v, want timestamp", consumedAt)
	}
}

func TestScheduleFollowOnRunDoesNotDuplicateConsumedHandoff(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	insertHandoffFixture(t, db, "message-1", "run-1", models.AgentRoleReviewer)

	handler := NewRunHandler(db, slog.Default(), nil)
	scheduled, _, _, err := handler.scheduleFollowOnRun(context.Background(), events.AgentRunEvent{RunID: "run-1", TaskID: "task-1"})
	if err != nil || !scheduled {
		t.Fatalf("first schedule = %v, %v", scheduled, err)
	}
	scheduled, _, _, err = handler.scheduleFollowOnRun(context.Background(), events.AgentRunEvent{RunID: "run-1", TaskID: "task-1"})
	if err != nil {
		t.Fatalf("second schedule error: %v", err)
	}
	if scheduled {
		t.Fatal("second schedule = true, want false")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs WHERE agent_role = ?`, models.AgentRoleReviewer).Scan(&count); err != nil {
		t.Fatalf("count reviewer runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("reviewer run count = %d, want 1", count)
	}
}

func TestScheduleFollowOnRunRejectsUnknownRole(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	insertHandoffFixture(t, db, "message-1", "run-1", "unknown_role")

	handler := NewRunHandler(db, slog.Default(), nil)
	_, _, _, err := handler.scheduleFollowOnRun(context.Background(), events.AgentRunEvent{RunID: "run-1", TaskID: "task-1"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "unknown agent role") {
		t.Fatalf("error = %v", err)
	}

	var consumedBy sql.NullString
	if err := db.QueryRow(`SELECT consumed_by_run_id FROM agent_messages WHERE id = 'message-1'`).Scan(&consumedBy); err != nil {
		t.Fatalf("query handoff: %v", err)
	}
	if consumedBy.Valid {
		t.Fatalf("consumed_by_run_id = %q, want null", consumedBy.String)
	}
}

func TestHandleRunCompletedReviewsWhenNoHandoff(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleReviewer)

	reviewService := &fakeReviewer{report: &reviewer.ReviewReport{
		RunID:      "run-1",
		RiskLevel:  "low",
		Approvable: true,
	}}
	handler := NewRunHandler(db, slog.Default(), nil).WithReviewer(reviewService)

	if err := handler.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)}); err != nil {
		t.Fatalf("HandleRunCompleted() error: %v", err)
	}
	if reviewService.runID != "run-1" {
		t.Fatalf("review runID = %q, want run-1", reviewService.runID)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&status); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if status != "reviewing" {
		t.Fatalf("task status = %q, want reviewing", status)
	}
}

func TestHandleRunCompletedRequiresReviewerWhenNoHandoff(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleReviewer)

	handler := NewRunHandler(db, slog.Default(), nil)
	err := handler.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "reviewer") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleRunCompletedReturnsReviewError(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleReviewer)

	handler := NewRunHandler(db, slog.Default(), nil).WithReviewer(&fakeReviewer{err: errors.New("review table missing")})
	err := handler.HandleRunCompleted(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "review table missing") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleRunFailedTransitionsTaskAndPublishesFailedEvent(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)

	publisher := &fakeWorkerEventPublisher{}
	handler := NewRunHandler(db, slog.Default(), nil).WithEventPublisher(publisher)

	msg := &nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1","agent_role":"implementer","status":"failed","data":{"error":"model provider unavailable"}}`)}
	if err := handler.HandleRunFailed(msg); err != nil {
		t.Fatalf("HandleRunFailed() error: %v", err)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&status); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if status != "failed" {
		t.Fatalf("task status = %q, want failed", status)
	}

	if publisher.subject != events.TaskFailed {
		t.Fatalf("published subject = %q, want %q", publisher.subject, events.TaskFailed)
	}
	if !contains(string(publisher.data), "task-1") || !contains(string(publisher.data), "failed") {
		t.Fatalf("published data = %q, want task failed event", string(publisher.data))
	}
}

func TestHandleRunFailedReturnsUnmarshalError(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()

	handler := NewRunHandler(db, slog.Default(), nil)
	err := handler.HandleRunFailed(&nats.Msg{Data: []byte(`not json`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "unmarshal") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleRunFailedReturnsPublishError(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)

	publisher := &fakeWorkerEventPublisher{err: errors.New("nats disconnected")}
	handler := NewRunHandler(db, slog.Default(), nil).WithEventPublisher(publisher)

	err := handler.HandleRunFailed(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "nats disconnected") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleRunTriggeredExecutesQueuedRun(t *testing.T) {
	executor := &fakeRunExecutor{}
	handler := NewRunHandler(nil, slog.Default(), nil).WithRunExecutor(executor)
	msg := &nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1","status":"queued"}`)}

	if err := handler.HandleRunTriggered(msg); err != nil {
		t.Fatalf("HandleRunTriggered() error: %v", err)
	}
	if executor.runID != "run-1" {
		t.Fatalf("executor runID = %q, want run-1", executor.runID)
	}
}

func TestHandleRunTriggeredRequiresExecutor(t *testing.T) {
	handler := NewRunHandler(nil, slog.Default(), nil)
	err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"run-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "executor") {
		t.Fatalf("error = %v", err)
	}
}

func TestHandleRunTriggeredReturnsExecutorError(t *testing.T) {
	executor := &fakeRunExecutor{err: errors.New("model provider unavailable")}
	handler := NewRunHandler(nil, slog.Default(), nil).WithRunExecutor(executor)
	err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"run-1"}`)})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "model provider unavailable") {
		t.Fatalf("error = %v", err)
	}
}

type fakeRunExecutor struct {
	runID string
	err   error
}

type fakeSchedulerStartBudget struct {
	allowed bool
	reason  string
	err     error
	runID   string
}

func (b *fakeSchedulerStartBudget) CheckRunStart(_ context.Context, runID string) (bool, string, error) {
	b.runID = runID
	return b.allowed, b.reason, b.err
}

func TestSchedulerAdmissionClaimsQueuedRun(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-a", "run-a", "queued", `{}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 1, CPU: 1, MemoryMB: 512})
	claimed, err := admission.claimRun(context.Background(), "run-a")
	if err != nil {
		t.Fatalf("claimRun() error: %v", err)
	}
	if !claimed {
		t.Fatal("claimRun() = false, want true for queued run")
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-a'`).Scan(&status); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if status != "admitting" {
		t.Fatalf("run status = %q, want admitting", status)
	}
}

func TestSchedulerAdmissionBlocksBudgetAfterAtomicClaim(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-a", "run-a", "queued", `{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}`)

	budget := &fakeSchedulerStartBudget{allowed: false, reason: "concurrent admitted runs 3 exceed max 2"}
	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192}).WithStartBudget(budget)
	decision, err := admission.AdmitRun(context.Background(), "run-a", "task-a")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed || !contains(decision.Reason, "budget-blocked") {
		t.Fatalf("decision = %#v, want budget-blocked", decision)
	}
	if budget.runID != "run-a" {
		t.Fatalf("budget runID = %q, want run-a", budget.runID)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-a'`).Scan(&status); err != nil {
		t.Fatalf("query run-a: %v", err)
	}
	if status != "queued" {
		t.Fatalf("run-a status = %q, want queued after denied admission releases claim", status)
	}
}

func TestSchedulerAdmissionPropagatesBudgetCheckFailure(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-a", "run-a", "queued", `{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192}).
		WithStartBudget(&fakeSchedulerStartBudget{err: errors.New("budget database unavailable")})
	_, err := admission.AdmitRun(context.Background(), "run-a", "task-a")
	if err == nil || !contains(err.Error(), "budget database unavailable") {
		t.Fatalf("AdmitRun() error = %v, want budget failure", err)
	}
}

func TestSchedulerAdmissionDeniesOwnershipConflictWithRunningRun(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-running", "run-running", "running", `{"scheduler":{"owns":["apps/api"],"cpu":2,"memory_mb":1024}}`)
	insertSchedulerTask(t, db, "task-candidate", "run-candidate", "queued", `{"scheduler":{"owns":["apps/api/routes"],"cpu":1,"memory_mb":512}}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("Allowed = true, want false")
	}
	if !contains(decision.Reason, "ownership-conflict") {
		t.Fatalf("Reason = %q", decision.Reason)
	}
}

func TestSchedulerAdmissionDerivesCandidateOwnershipFromTaskSpec(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-running", "run-running", "running", `{"scheduler":{"owns":["apps/api"],"cpu":2,"memory_mb":1024}}`)
	insertSchedulerTask(t, db, "task-candidate", "run-candidate", "queued", `{}`)
	insertSchedulerTaskSpec(t, db, "task-candidate", []string{"apps/api/routes/campaigns/handler.go"}, []string{"apps/api/routes/campaigns/new.go"})

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed || !contains(decision.Reason, "ownership-conflict") {
		t.Fatalf("decision = %#v, want ownership conflict derived from task spec", decision)
	}
}

func TestSchedulerAdmissionDerivesRunningOwnershipFromTaskSpec(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-running", "run-running", "running", `{}`)
	insertSchedulerTaskSpec(t, db, "task-running", []string{"apps/api"}, nil)
	insertSchedulerTask(t, db, "task-candidate", "run-candidate", "queued", `{"scheduler":{"owns":["apps/api/routes"],"cpu":1,"memory_mb":512}}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed || !contains(decision.Reason, "ownership-conflict") {
		t.Fatalf("decision = %#v, want ownership conflict derived from running task spec", decision)
	}
}

func TestSchedulerAdmissionRejectsBlockedPersistedReadiness(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTaskWithRunMetadata(
		t, db, "task-candidate", "run-candidate", "queued", `{}`,
		`{"admission":{"policy":"task-readiness-v1","readiness":{"status":"blocked","checks":[]}}}`,
	)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed || decision.Reason != "readiness-blocked" {
		t.Fatalf("decision = %#v, want readiness-blocked", decision)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-candidate'`).Scan(&status); err != nil {
		t.Fatalf("query run status: %v", err)
	}
	if status != "queued" {
		t.Fatalf("run status = %q, want queued", status)
	}
}

func TestSchedulerAdmissionChecksPersistedReadinessBeforeMutableSchedulerMetadata(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTaskWithRunMetadata(
		t, db, "task-candidate", "run-candidate", "queued", `{"scheduler":`,
		`{"admission":{"policy":"task-readiness-v1","readiness":{"status":"blocked","checks":[]}}}`,
	)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed || decision.Reason != "readiness-blocked" {
		t.Fatalf("decision = %#v, want readiness-blocked", decision)
	}
}

func TestSchedulerAdmissionAcceptsPersistedAttentionReadiness(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTaskWithRunMetadata(
		t, db, "task-candidate", "run-candidate", "queued", `{}`,
		`{"admission":{"policy":"task-readiness-v1","readiness":{"status":"attention","checks":[]}}}`,
	)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("decision = %#v, want allowed", decision)
	}
}

func TestSchedulerAdmissionRejectsUnknownPersistedAdmissionPolicy(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTaskWithRunMetadata(
		t, db, "task-candidate", "run-candidate", "queued", `{}`,
		`{"admission":{"policy":"task-readiness-v999","readiness":{"status":"ready","checks":[]}}}`,
	)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed || decision.Reason != "unsupported-admission-policy" {
		t.Fatalf("decision = %#v, want unsupported-admission-policy", decision)
	}
}

func TestSchedulerAdmissionKeepsLegacyBypassWithoutMetadataOrTaskSpec(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-candidate", "run-candidate", "queued", `{}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("decision = %#v, want legacy bypass", decision)
	}
}

func TestSchedulerAdmissionAtomicClaimBlocksSecondOwnershipConflict(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-a", "run-a", "queued", `{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}`)
	insertSchedulerTask(t, db, "task-b", "run-b", "queued", `{"scheduler":{"owns":["apps/api/routes"],"cpu":1,"memory_mb":512}}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	first, err := admission.AdmitRun(context.Background(), "run-a", "task-a")
	if err != nil || !first.Allowed {
		t.Fatalf("first admission = %#v, %v", first, err)
	}
	second, err := admission.AdmitRun(context.Background(), "run-b", "task-b")
	if err != nil {
		t.Fatalf("second AdmitRun() error: %v", err)
	}
	if second.Allowed || !contains(second.Reason, "ownership-conflict") {
		t.Fatalf("second admission = %#v, want ownership conflict", second)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-a'`).Scan(&status); err != nil {
		t.Fatalf("query run-a: %v", err)
	}
	if status != "admitting" {
		t.Fatalf("run-a status = %q, want admitting", status)
	}
}

func TestSchedulerAdmissionReclaimsStaleAdmissionClaim(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-a", "run-a", "queued", `{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}`)
	stale := time.Now().UTC().Add(-schedulerAdmissionClaimTTL - time.Minute)
	if _, err := db.Exec(`UPDATE agent_runs SET status = 'admitting', updated_at = ? WHERE id = 'run-a'`, stale); err != nil {
		t.Fatalf("mark stale admitting: %v", err)
	}

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-a", "task-a")
	if err != nil || !decision.Allowed {
		t.Fatalf("stale admission = %#v, %v", decision, err)
	}
}

func TestSchedulerAdmissionDoesNotStealFreshAdmissionClaim(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-a", "run-a", "queued", `{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}`)
	if _, err := db.Exec(`UPDATE agent_runs SET status = 'admitting', updated_at = ? WHERE id = 'run-a'`, time.Now().UTC()); err != nil {
		t.Fatalf("mark fresh admitting: %v", err)
	}

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-a", "task-a")
	if err != nil {
		t.Fatalf("fresh AdmitRun() error: %v", err)
	}
	if decision.Allowed || decision.Reason != "run-already-claimed" {
		t.Fatalf("fresh admission = %#v, want run-already-claimed", decision)
	}
}

func TestSchedulerAdmissionReleaseRestoresOnlyAdmittingRun(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-a", "run-a", "queued", `{"scheduler":{"owns":["apps/api"],"cpu":1,"memory_mb":512}}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-a", "task-a")
	if err != nil || !decision.Allowed {
		t.Fatalf("admission = %#v, %v", decision, err)
	}
	if err := admission.ReleaseRun(context.Background(), "run-a"); err != nil {
		t.Fatalf("ReleaseRun() error: %v", err)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-a'`).Scan(&status); err != nil {
		t.Fatalf("query run-a: %v", err)
	}
	if status != "queued" {
		t.Fatalf("run-a status = %q, want queued", status)
	}

	if _, err := db.Exec(`UPDATE agent_runs SET status = 'running' WHERE id = 'run-a'`); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if err := admission.ReleaseRun(context.Background(), "run-a"); err != nil {
		t.Fatalf("ReleaseRun running: %v", err)
	}
	if err := db.QueryRow(`SELECT status FROM agent_runs WHERE id = 'run-a'`).Scan(&status); err != nil {
		t.Fatalf("query running run-a: %v", err)
	}
	if status != "running" {
		t.Fatalf("running run-a status = %q, want running", status)
	}
}

func TestSchedulerAdmissionAllowsDisjointRunWithinCapacity(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-running", "run-running", "running", `{"scheduler":{"owns":["apps/api"],"cpu":2,"memory_mb":1024}}`)
	insertSchedulerTask(t, db, "task-candidate", "run-candidate", "queued", `{"scheduler":{"owns":["apps/web"],"cpu":1,"memory_mb":512}}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("Allowed = false, reason %q", decision.Reason)
	}
}

func TestSchedulerAdmissionDeniesWhenDependencyIsNotDone(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-dependency", "", "completed", `{"scheduler":{"owns":["packages/shared"],"cpu":1,"memory_mb":256}}`)
	insertSchedulerTask(t, db, "task-candidate", "run-candidate", "queued", `{"scheduler":{"owns":["apps/web"],"depends_on":["task-dependency"],"cpu":1,"memory_mb":512}}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if decision.Allowed {
		t.Fatal("Allowed = true, want false")
	}
	if !contains(decision.Reason, "dependency-pending") {
		t.Fatalf("Reason = %q", decision.Reason)
	}
}

func TestSchedulerAdmissionAllowsWhenDependencyIsDone(t *testing.T) {
	db := setupSchedulerAdmissionDB(t)
	defer db.Close()
	insertSchedulerTask(t, db, "task-dependency", "", "done", `{"scheduler":{"owns":["packages/shared"],"cpu":1,"memory_mb":256}}`)
	insertSchedulerTask(t, db, "task-candidate", "run-candidate", "queued", `{"scheduler":{"owns":["apps/web"],"depends_on":["task-dependency"],"cpu":1,"memory_mb":512}}`)

	admission := NewSchedulerAdmission(db, SchedulerCapacity{MaxParallel: 4, CPU: 8, MemoryMB: 8192})
	decision, err := admission.AdmitRun(context.Background(), "run-candidate", "task-candidate")
	if err != nil {
		t.Fatalf("AdmitRun() error: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("Allowed = false, reason %q", decision.Reason)
	}
}

func setupSchedulerAdmissionDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT 'project-1',
			repository_id TEXT NOT NULL DEFAULT 'repo-1',
			status TEXT NOT NULL,
			metadata TEXT DEFAULT '{}',
			deleted_at DATETIME
		);
		CREATE TABLE task_specs (
			task_id TEXT PRIMARY KEY,
			files_to_change TEXT DEFAULT '[]',
			files_to_create TEXT DEFAULT '[]'
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			status TEXT NOT NULL,
			metadata TEXT DEFAULT '{}',
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		t.Fatalf("create scheduler schema: %v", err)
	}
	return db
}

func insertSchedulerTask(t *testing.T, db *sql.DB, taskID, runID, status, metadata string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tasks (id, status, metadata) VALUES (?, ?, ?)`, taskID, status, metadata); err != nil {
		t.Fatalf("insert task %s: %v", taskID, err)
	}
	if runID != "" {
		if _, err := db.Exec(`INSERT INTO agent_runs (id, task_id, status) VALUES (?, ?, ?)`, runID, taskID, mapRunStatus(status)); err != nil {
			t.Fatalf("insert run %s: %v", runID, err)
		}
	}
}

func insertSchedulerTaskWithRunMetadata(t *testing.T, db *sql.DB, taskID, runID, taskStatus, taskMetadata, runMetadata string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tasks (id, status, metadata) VALUES (?, ?, ?)`, taskID, taskStatus, taskMetadata); err != nil {
		t.Fatalf("insert task %s: %v", taskID, err)
	}
	if _, err := db.Exec(`INSERT INTO agent_runs (id, task_id, status, metadata) VALUES (?, ?, ?, ?)`, runID, taskID, mapRunStatus(taskStatus), runMetadata); err != nil {
		t.Fatalf("insert run %s: %v", runID, err)
	}
}

func insertSchedulerTaskSpec(t *testing.T, db *sql.DB, taskID string, filesToChange, filesToCreate []string) {
	t.Helper()
	changed, _ := json.Marshal(filesToChange)
	created, _ := json.Marshal(filesToCreate)
	if _, err := db.Exec(`INSERT INTO task_specs (task_id, files_to_change, files_to_create) VALUES (?, ?, ?)`, taskID, string(changed), string(created)); err != nil {
		t.Fatalf("insert task spec %s: %v", taskID, err)
	}
}

func mapRunStatus(taskStatus string) string {
	if taskStatus == "running" {
		return "running"
	}
	return "queued"
}

func TestHandleRunTriggeredRejectsRunWhenAdmissionDenies(t *testing.T) {
	executor := &fakeRunExecutor{}
	admission := &fakeRunAdmission{decision: RunAdmissionDecision{Allowed: false, Reason: "ownership-conflict"}}
	handler := NewRunHandler(nil, slog.Default(), nil).
		WithRunExecutor(executor).
		WithRunAdmission(admission)

	err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)})
	if err == nil {
		t.Fatal("expected admission error")
	}
	if !errors.Is(err, ErrRunAdmissionDeferred) {
		t.Fatalf("error = %v, want ErrRunAdmissionDeferred", err)
	}
	if !contains(err.Error(), "ownership-conflict") {
		t.Fatalf("error = %v", err)
	}
	if executor.runID != "" {
		t.Fatalf("executor received run %q, want no execution", executor.runID)
	}
	if admission.runID != "run-1" || admission.taskID != "task-1" {
		t.Fatalf("admission saw run=%q task=%q", admission.runID, admission.taskID)
	}
}

func TestHandleRunTriggeredExecutesRunWhenAdmissionAllows(t *testing.T) {
	executor := &fakeRunExecutor{}
	admission := &fakeRunAdmission{decision: RunAdmissionDecision{Allowed: true}}
	handler := NewRunHandler(nil, slog.Default(), nil).
		WithRunExecutor(executor).
		WithRunAdmission(admission)

	if err := handler.HandleRunTriggered(&nats.Msg{Data: []byte(`{"run_id":"run-1","task_id":"task-1"}`)}); err != nil {
		t.Fatalf("HandleRunTriggered() error: %v", err)
	}
	if executor.runID != "run-1" {
		t.Fatalf("executor runID = %q, want run-1", executor.runID)
	}
}

type fakeRunAdmission struct {
	runID    string
	taskID   string
	decision RunAdmissionDecision
	err      error
}

func (a *fakeRunAdmission) AdmitRun(ctx context.Context, runID, taskID string) (RunAdmissionDecision, error) {
	a.runID = runID
	a.taskID = taskID
	return a.decision, a.err
}

func (a *fakeRunAdmission) ReleaseRun(ctx context.Context, runID string) error {
	return nil
}

func (e *fakeRunExecutor) ExecuteRun(ctx context.Context, runID string) error {
	e.runID = runID
	return e.err
}

type fakeReviewer struct {
	runID  string
	report *reviewer.ReviewReport
	err    error
}

func (r *fakeReviewer) Review(ctx context.Context, runID string) (*reviewer.ReviewReport, error) {
	r.runID = runID
	if r.err != nil {
		return nil, r.err
	}
	if r.report != nil {
		return r.report, nil
	}
	return &reviewer.ReviewReport{RunID: runID, RiskLevel: "low", Approvable: true}, nil
}

func setupRunHandlerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE tasks (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			deleted_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE agent_runs (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			workspace_id TEXT,
			agent_role TEXT NOT NULL,
			model TEXT,
			provider TEXT,
			status TEXT NOT NULL,
			total_cost REAL DEFAULT 0,
			metadata TEXT DEFAULT '{}',
			created_at DATETIME,
			updated_at DATETIME
		);
		CREATE TABLE agent_messages (
			id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			agent_run_id TEXT,
			from_agent TEXT NOT NULL,
			to_agent TEXT NOT NULL,
			message_type TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT DEFAULT '{}',
			consumed_at DATETIME,
			consumed_by_run_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create schema: %v", err)
	}
	return db
}

func insertCompletedRunFixture(t *testing.T, db *sql.DB, role string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO tasks (id, status) VALUES ('task-1', 'running');
		INSERT INTO agent_runs (
			id, task_id, workspace_id, agent_role, model, provider, status, total_cost, metadata
		) VALUES (
			'run-1', 'task-1', 'workspace-1', ?, 'gpt-4o', 'openai', 'completed', 0,
			'{"admission":{"policy":"task-readiness-v1","readiness":{"status":"ready","checks":[]}}}'
		);
	`, role)
	if err != nil {
		t.Fatalf("insert completed run fixture: %v", err)
	}
}

func insertHandoffFixture(t *testing.T, db *sql.DB, id, runID, toAgent string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO agent_messages (
			id, task_id, agent_run_id, from_agent, to_agent, message_type, content, metadata
		) VALUES (
			?, 'task-1', ?, 'implementer', ?, 'handoff', 'review this change', '{}'
		)
	`, id, runID, toAgent)
	if err != nil {
		t.Fatalf("insert handoff fixture: %v", err)
	}
}

func contains(value, substr string) bool {
	return strings.Contains(value, substr)
}

func TestHandleRunFailedSchedulesBoundedAutomaticRetry(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	if _, err := db.Exec(`UPDATE agent_runs SET status = 'failed' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("mark run failed: %v", err)
	}

	publisher := &fakeWorkerEventPublisher{}
	handler := NewRunHandler(db, slog.Default(), nil).WithEventPublisher(publisher)
	msg := failedRunMessage(t, "run-1", "task-1", runfailure.Classification{
		Taxonomy:    runfailure.TaxonomyVersion,
		Category:    runfailure.CategoryInfrastructure,
		Retryable:   true,
		Disposition: runfailure.DispositionRetry,
		Stage:       "verification",
		Source:      "tests",
	})

	if err := handler.HandleRunFailed(msg); err != nil {
		t.Fatalf("HandleRunFailed() error: %v", err)
	}

	var taskStatus string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if taskStatus != "running" {
		t.Fatalf("task status = %q, want running", taskStatus)
	}

	var retryRunID, retryStatus, retryMetadata string
	if err := db.QueryRow(`
		SELECT id, status, metadata
		FROM agent_runs
		WHERE id <> 'run-1'
		LIMIT 1
	`).Scan(&retryRunID, &retryStatus, &retryMetadata); err != nil {
		t.Fatalf("query retry run: %v", err)
	}
	if retryRunID == "" || retryStatus != "queued" {
		t.Fatalf("retry run = id %q status %q", retryRunID, retryStatus)
	}
	var metadata struct {
		Failure any `json:"failure"`
		Retry   struct {
			OriginalRunID string                    `json:"original_run_id"`
			AutoAttempt   int                       `json:"auto_attempt"`
			Previous      runfailure.Classification `json:"previous_failure"`
		} `json:"retry"`
	}
	if err := json.Unmarshal([]byte(retryMetadata), &metadata); err != nil {
		t.Fatalf("decode retry metadata: %v", err)
	}
	if metadata.Failure != nil {
		t.Fatalf("new run inherited active failure: %#v", metadata.Failure)
	}
	if metadata.Retry.OriginalRunID != "run-1" || metadata.Retry.AutoAttempt != 1 {
		t.Fatalf("retry metadata = %+v", metadata.Retry)
	}
	if metadata.Retry.Previous.Category != runfailure.CategoryInfrastructure {
		t.Fatalf("previous failure = %+v", metadata.Retry.Previous)
	}
	if publisher.subject != events.RunTriggered {
		t.Fatalf("published subject = %q, want %q", publisher.subject, events.RunTriggered)
	}
	if !contains(string(publisher.data), retryRunID) {
		t.Fatalf("published retry event = %s, want run %s", publisher.data, retryRunID)
	}
}

func TestHandleRunFailedStopsAutomaticRetryAtBudget(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	if _, err := db.Exec(`
		UPDATE agent_runs
		SET status = 'failed', metadata = '{"retry":{"auto_attempt":2}}'
		WHERE id = 'run-1'
	`); err != nil {
		t.Fatalf("mark retried run failed: %v", err)
	}

	publisher := &fakeWorkerEventPublisher{}
	handler := NewRunHandler(db, slog.Default(), nil).WithEventPublisher(publisher)
	msg := failedRunMessage(t, "run-1", "task-1", runfailure.Classification{
		Taxonomy:    runfailure.TaxonomyVersion,
		Category:    runfailure.CategoryInfrastructure,
		Retryable:   true,
		Disposition: runfailure.DispositionRetry,
		Stage:       "verification",
		Source:      "tests",
	})

	if err := handler.HandleRunFailed(msg); err != nil {
		t.Fatalf("HandleRunFailed() error: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("run count = %d, want 1 after exhausted retry budget", count)
	}
	var taskStatus string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if taskStatus != "failed" {
		t.Fatalf("task status = %q, want failed", taskStatus)
	}
	if publisher.subject != events.TaskFailed {
		t.Fatalf("published subject = %q, want %q", publisher.subject, events.TaskFailed)
	}
}

func TestHandleRunFailedDoesNotAutoRetryFreshEnvironmentDisposition(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	if _, err := db.Exec(`UPDATE agent_runs SET status = 'failed' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("mark run failed: %v", err)
	}

	publisher := &fakeWorkerEventPublisher{}
	handler := NewRunHandler(db, slog.Default(), nil).WithEventPublisher(publisher)
	msg := failedRunMessage(t, "run-1", "task-1", runfailure.Classification{
		Taxonomy:    runfailure.TaxonomyVersion,
		Category:    runfailure.CategoryResource,
		Retryable:   true,
		Disposition: runfailure.DispositionRetryFreshEnvironment,
		Stage:       "verification",
		Source:      "build",
	})

	if err := handler.HandleRunFailed(msg); err != nil {
		t.Fatalf("HandleRunFailed() error: %v", err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("run count = %d, want no automatic fresh-environment retry", count)
	}
	if publisher.subject != events.TaskFailed {
		t.Fatalf("published subject = %q, want %q", publisher.subject, events.TaskFailed)
	}
}

func failedRunMessage(t *testing.T, runID, taskID string, classification runfailure.Classification) *nats.Msg {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"error":   "verification failed",
		"failure": classification,
	})
	if err != nil {
		t.Fatalf("marshal failure data: %v", err)
	}
	event, err := json.Marshal(events.AgentRunEvent{
		RunID:     runID,
		TaskID:    taskID,
		AgentRole: models.AgentRoleImplementer,
		Status:    models.AgentRunStatusFailed,
		Data:      data,
	})
	if err != nil {
		t.Fatalf("marshal failed run event: %v", err)
	}
	return &nats.Msg{Data: event}
}

func TestHandleRunFailedAutomaticRetryIsIdempotentAcrossRedelivery(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	if _, err := db.Exec(`UPDATE agent_runs SET status = 'failed' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("mark run failed: %v", err)
	}

	publisher := &fakeWorkerEventPublisher{}
	handler := NewRunHandler(db, slog.Default(), nil).WithEventPublisher(publisher)
	classification := runfailure.Classification{
		Taxonomy:    runfailure.TaxonomyVersion,
		Category:    runfailure.CategoryInfrastructure,
		Retryable:   true,
		Disposition: runfailure.DispositionRetry,
		Stage:       "verification",
		Source:      "tests",
	}

	for i := 0; i < 2; i++ {
		if err := handler.HandleRunFailed(failedRunMessage(t, "run-1", "task-1", classification)); err != nil {
			t.Fatalf("HandleRunFailed() delivery %d error: %v", i+1, err)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&count); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if count != 2 {
		t.Fatalf("run count = %d, want original + one deterministic retry", count)
	}
	if publisher.count != 1 {
		t.Fatalf("publish count = %d, want one runs.triggered publication", publisher.count)
	}
}


func TestHandleRunFailedRetriesDispatchAfterPublishFailure(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	if _, err := db.Exec(`UPDATE agent_runs SET status = 'failed' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("mark run failed: %v", err)
	}

	publisher := &fakeWorkerEventPublisher{err: errors.New("nats unavailable")}
	handler := NewRunHandler(db, slog.Default(), nil).WithEventPublisher(publisher)
	classification := runfailure.Classification{
		Taxonomy:    runfailure.TaxonomyVersion,
		Category:    runfailure.CategoryInfrastructure,
		Retryable:   true,
		Disposition: runfailure.DispositionRetry,
		Stage:       "verification",
		Source:      "tests",
	}

	err := handler.HandleRunFailed(failedRunMessage(t, "run-1", "task-1", classification))
	if err == nil || !contains(err.Error(), "nats unavailable") {
		t.Fatalf("first delivery error = %v, want publish failure", err)
	}

	var retryRunID string
	if err := db.QueryRow(`SELECT id FROM agent_runs WHERE id <> 'run-1'`).Scan(&retryRunID); err != nil {
		t.Fatalf("query persisted retry run: %v", err)
	}

	publisher.err = nil
	if err := handler.HandleRunFailed(failedRunMessage(t, "run-1", "task-1", classification)); err != nil {
		t.Fatalf("redelivery error: %v", err)
	}
	if publisher.count != 2 {
		t.Fatalf("publish attempts = %d, want failed attempt plus successful redelivery", publisher.count)
	}
	if !contains(string(publisher.data), retryRunID) {
		t.Fatalf("published data = %s, want retry run %s", publisher.data, retryRunID)
	}

	var metadata string
	if err := db.QueryRow(`SELECT metadata FROM agent_runs WHERE id = ?`, retryRunID).Scan(&metadata); err != nil {
		t.Fatalf("query retry metadata: %v", err)
	}
	var envelope struct {
		Retry struct {
			DispatchPublished bool `json:"dispatch_published"`
		} `json:"retry"`
	}
	if err := json.Unmarshal([]byte(metadata), &envelope); err != nil {
		t.Fatalf("decode retry metadata: %v", err)
	}
	if !envelope.Retry.DispatchPublished {
		t.Fatalf("retry metadata = %s, want dispatch_published=true", metadata)
	}
}


func TestHandleRunFailedDoesNotAutoRetryWithoutEventPublisher(t *testing.T) {
	db := setupRunHandlerDB(t)
	defer db.Close()
	insertCompletedRunFixture(t, db, models.AgentRoleImplementer)
	if _, err := db.Exec(`UPDATE agent_runs SET status = 'failed' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("mark run failed: %v", err)
	}

	handler := NewRunHandler(db, slog.Default(), nil)
	msg := failedRunMessage(t, "run-1", "task-1", runfailure.Classification{
		Taxonomy:    runfailure.TaxonomyVersion,
		Category:    runfailure.CategoryInfrastructure,
		Retryable:   true,
		Disposition: runfailure.DispositionRetry,
		Stage:       "verification",
		Source:      "tests",
	})
	if err := handler.HandleRunFailed(msg); err != nil {
		t.Fatalf("HandleRunFailed() error: %v", err)
	}

	var runCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_runs`).Scan(&runCount); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("run count = %d, want no automatic retry without publisher", runCount)
	}
	var taskStatus string
	if err := db.QueryRow(`SELECT status FROM tasks WHERE id = 'task-1'`).Scan(&taskStatus); err != nil {
		t.Fatalf("query task status: %v", err)
	}
	if taskStatus != "failed" {
		t.Fatalf("task status = %q, want failed", taskStatus)
	}
}
