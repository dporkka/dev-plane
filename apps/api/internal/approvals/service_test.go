package approvals

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/models"
)

func TestRequestApproval(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	svc := NewService(db, nil, nil, slog.Default())

	taskID := "task-1"

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	mock.ExpectExec("INSERT INTO approvals").
		WillReturnResult(sqlmock.NewResult(1, 1))

	approval, err := svc.RequestApproval(context.Background(), taskID, "pr_create", "user-1", nil)
	if err != nil {
		t.Fatalf("RequestApproval() error: %v", err)
	}
	if approval == nil {
		t.Fatal("expected non-nil approval")
	}
	if approval.TaskID != taskID {
		t.Errorf("expected task ID %q, got %q", taskID, approval.TaskID)
	}
	if approval.ApprovalType != "pr_create" {
		t.Errorf("expected type 'pr_create', got %q", approval.ApprovalType)
	}
	if approval.Response != nil {
		t.Errorf("expected nil response on new approval, got %q", *approval.Response)
	}
}

func TestRequestApproval_TaskNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	svc := NewService(db, nil, nil, slog.Default())

	taskID := "nonexistent"

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	_, err = svc.RequestApproval(context.Background(), taskID, "pr_create", "user-1", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestGetPendingApprovals(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	svc := NewService(db, nil, nil, slog.Default())

	taskID := "task-1"
	now := time.Now()

	mock.ExpectQuery("SELECT id, task_id, agent_run_id").
		WithArgs(taskID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "task_id", "agent_run_id", "approval_type", "requested_by", "requested_at",
			"responded_by", "response", "response_note", "responded_at", "expires_at",
			"metadata", "created_at", "updated_at",
		}).AddRow(
			"approval-1", taskID, nil, "pr_create", "user-1", now,
			nil, nil, nil, nil, nil,
			nil, now, now,
		))

	approvals, err := svc.GetPendingApprovals(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetPendingApprovals() error: %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("expected 1 approval, got %d", len(approvals))
	}
	if approvals[0].ID != "approval-1" {
		t.Errorf("expected approval ID 'approval-1', got %q", approvals[0].ID)
	}
	if !approvals[0].IsPending() {
		t.Error("expected approval to be pending")
	}
}

func TestRespondApproval_InvalidResponse(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	svc := NewService(db, nil, nil, slog.Default())

	err = svc.RespondApproval(context.Background(), "approval-1", "user-1", "invalid", "")
	if err == nil {
		t.Fatal("expected error for invalid response")
	}
}

func TestRequestApproval_WithDetails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	svc := NewService(db, nil, nil, slog.Default())

	taskID := "task-2"
	details := map[string]any{"reason": "security fix", "severity": "high"}

	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

	mock.ExpectExec("INSERT INTO approvals").
		WillReturnResult(sqlmock.NewResult(1, 1))

	approval, err := svc.RequestApproval(context.Background(), taskID, "deploy", "user-2", details)
	if err != nil {
		t.Fatalf("RequestApproval() with details error: %v", err)
	}
	if approval.Metadata == nil {
		t.Error("expected metadata to be set for approval with details")
	}
	if approval.ApprovalType != "deploy" {
		t.Errorf("expected type 'deploy', got %q", approval.ApprovalType)
	}
}

func TestApprovalExpiryCheck(t *testing.T) {
	expiredAt := time.Now().Add(-1 * time.Minute)
	approval := &models.Approval{
		ID:        "exp-1",
		ExpiresAt: &expiredAt,
	}
	if !approval.IsExpired() {
		t.Error("expected expired approval to report as expired")
	}

	future := time.Now().Add(1 * time.Hour)
	approval.ExpiresAt = &future
	if approval.IsExpired() {
		t.Error("expected future expiry to not be expired")
	}
}
