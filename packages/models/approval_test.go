package models

import (
	"testing"
	"time"
)

func TestApprovalType_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{ApprovalTypeSpec, "spec"},
		{ApprovalTypeExecution, "execution"},
		{ApprovalTypePRCreate, "pr_create"},
		{ApprovalTypeDeploy, "deploy"},
		{ApprovalTypeRiskyAction, "risky_action"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestApprovalStatus_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{ApprovalResponseApproved, "approved"},
		{ApprovalResponseRejected, "rejected"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestApproval_IsPending(t *testing.T) {
	t.Run("pending when no response", func(t *testing.T) {
		a := &Approval{Response: nil}
		assertEqual(t, a.IsPending(), true)
	})

	t.Run("not pending when responded", func(t *testing.T) {
		response := "approved"
		a := &Approval{Response: &response}
		assertEqual(t, a.IsPending(), false)
	})
}

func TestApproval_IsExpired(t *testing.T) {
	t.Run("not expired when no expiration", func(t *testing.T) {
		a := &Approval{ExpiresAt: nil}
		assertEqual(t, a.IsExpired(), false)
	})

	t.Run("expired when past expiration", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		a := &Approval{ExpiresAt: &past}
		assertEqual(t, a.IsExpired(), true)
	})

	t.Run("not expired when future expiration", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		a := &Approval{ExpiresAt: &future}
		assertEqual(t, a.IsExpired(), false)
	})
}

func TestApproval_Validate(t *testing.T) {
	t.Run("valid approval", func(t *testing.T) {
		a := &Approval{
			TaskID:       "task-1",
			ApprovalType: ApprovalTypeSpec,
			RequestedBy:  "user-1",
		}
		assertError(t, a.Validate(), false)
	})

	t.Run("missing task_id", func(t *testing.T) {
		a := &Approval{
			ApprovalType: ApprovalTypeSpec,
			RequestedBy:  "user-1",
		}
		err := a.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "approval task_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "approval task_id is required")
		}
	})

	t.Run("missing approval_type", func(t *testing.T) {
		a := &Approval{
			TaskID:      "task-1",
			RequestedBy: "user-1",
		}
		err := a.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "approval approval_type is required" {
			t.Errorf("got %q, want %q", err.Error(), "approval approval_type is required")
		}
	})

	t.Run("missing requested_by", func(t *testing.T) {
		a := &Approval{
			TaskID:       "task-1",
			ApprovalType: ApprovalTypeSpec,
		}
		err := a.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "approval requested_by is required" {
			t.Errorf("got %q, want %q", err.Error(), "approval requested_by is required")
		}
	})
}
