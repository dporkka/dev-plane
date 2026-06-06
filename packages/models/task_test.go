package models

import "testing"

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

func assertError(t *testing.T, err error, wantErr bool) {
	t.Helper()
	if (err != nil) != wantErr {
		t.Errorf("error = %v, wantErr %v", err, wantErr)
	}
}

func TestTaskStatus_Constants(t *testing.T) {
	tests := []struct {
		name  string
		got   TaskStatus
		want  string
	}{
		{"Backlog", TaskStatusBacklog, "backlog"},
		{"SpecReview", TaskStatusSpecReview, "spec_review"},
		{"Approved", TaskStatusApproved, "approved"},
		{"Running", TaskStatusRunning, "running"},
		{"Reviewing", TaskStatusReviewing, "reviewing"},
		{"PRCreated", TaskStatusPRCreated, "pr_created"},
		{"Done", TaskStatusDone, "done"},
		{"Failed", TaskStatusFailed, "failed"},
		{"Cancelled", TaskStatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertEqual(t, string(tt.got), tt.want)
		})
	}
}

func TestTaskStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   bool
	}{
		{TaskStatusDone, true},
		{TaskStatusFailed, true},
		{TaskStatusCancelled, true},
		{TaskStatusBacklog, false},
		{TaskStatusSpecReview, false},
		{TaskStatusApproved, false},
		{TaskStatusRunning, false},
		{TaskStatusReviewing, false},
		{TaskStatusPRCreated, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			task := &Task{Status: tt.status}
			assertEqual(t, task.IsTerminal(), tt.want)
		})
	}
}

func TestTaskStatus_IsActive(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   bool
	}{
		{TaskStatusRunning, true},
		{TaskStatusReviewing, true},
		{TaskStatusBacklog, false},
		{TaskStatusSpecReview, false},
		{TaskStatusApproved, false},
		{TaskStatusPRCreated, false},
		{TaskStatusDone, false},
		{TaskStatusFailed, false},
		{TaskStatusCancelled, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			task := &Task{Status: tt.status}
			assertEqual(t, task.IsActive(), tt.want)
		})
	}
}

func TestPriority_Constants(t *testing.T) {
	tests := []struct {
		got  Priority
		want string
	}{
		{PriorityLow, "low"},
		{PriorityMedium, "medium"},
		{PriorityHigh, "high"},
		{PriorityUrgent, "urgent"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, string(tt.got), tt.want)
		})
	}
}

func TestRiskLevel_Constants(t *testing.T) {
	tests := []struct {
		got  RiskLevel
		want string
	}{
		{RiskLevelLow, "low"},
		{RiskLevelMedium, "medium"},
		{RiskLevelHigh, "high"},
		{RiskLevelCritical, "critical"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, string(tt.got), tt.want)
		})
	}
}

func TestTaskSource_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{TaskSourceWeb, "web"},
		{TaskSourceGitHub, "github_issue"},
		{TaskSourceLinear, "linear"},
		{TaskSourceSlack, "slack"},
		{TaskSourceDiscord, "discord"},
		{TaskSourceWebhook, "webhook"},
		{TaskSourceVoice, "voice"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestTask_Validate(t *testing.T) {
	t.Run("valid task", func(t *testing.T) {
		task := &Task{
			Title:        "Test Task",
			ProjectID:    "proj-1",
			RepositoryID: "repo-1",
			CreatedBy:    "user-1",
		}
		assertError(t, task.Validate(), false)
	})

	t.Run("missing title", func(t *testing.T) {
		task := &Task{
			ProjectID:    "proj-1",
			RepositoryID: "repo-1",
			CreatedBy:    "user-1",
		}
		err := task.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "task title is required" {
			t.Errorf("got %q, want %q", err.Error(), "task title is required")
		}
	})

	t.Run("missing project", func(t *testing.T) {
		task := &Task{
			Title:        "Test Task",
			RepositoryID: "repo-1",
			CreatedBy:    "user-1",
		}
		err := task.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "task project_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "task project_id is required")
		}
	})
}

func TestTask_Validate_MissingRepo(t *testing.T) {
	task := &Task{
		Title:     "Test Task",
		ProjectID: "proj-1",
		CreatedBy: "user-1",
	}
	err := task.Validate()
	assertError(t, err, true)
	if err != nil && err.Error() != "task repository_id is required" {
		t.Errorf("got %q, want %q", err.Error(), "task repository_id is required")
	}
}

func TestTask_Validate_MissingCreatedBy(t *testing.T) {
	task := &Task{
		Title:        "Test Task",
		ProjectID:    "proj-1",
		RepositoryID: "repo-1",
	}
	err := task.Validate()
	assertError(t, err, true)
	if err != nil && err.Error() != "task created_by is required" {
		t.Errorf("got %q, want %q", err.Error(), "task created_by is required")
	}
}
