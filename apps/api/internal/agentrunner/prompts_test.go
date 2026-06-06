package agentrunner

import (
	"testing"

	"github.com/ai-dev-control-plane/models"
)

func TestBuildSystemPrompt(t *testing.T) {
	task := &models.Task{
		ID:           "task-1",
		ProjectID:    "proj-1",
		RepositoryID: "repo-1",
		CreatedBy:    "user-1",
		Source:       "web",
		Title:        "Add user authentication",
		Status:       models.TaskStatusApproved,
		Priority:     models.PriorityHigh,
		RiskLevel:    models.RiskLevelMedium,
		TargetBranch: "main",
	}

	ws := &models.Workspace{
		ID:           "ws-1",
		RepositoryID: "repo-1",
		Name:         "test-ws",
		Branch:       "feature/auth",
		BaseBranch:   "main",
		Status:       models.WorkspaceStatusReady,
	}

	prompt := BuildSystemPrompt(models.AgentRoleImplementer, task, ws)

	if prompt == "" {
		t.Fatal("BuildSystemPrompt returned empty string")
	}

	// Check key sections are present
	requiredSections := []string{
		"You are a senior software engineering agent",
		"Add user authentication",
		"Available Tools",
		"read_file",
		"write_file",
		"search_files",
		"list_directory",
		"apply_patch",
		"run_command",
		"inspect_repo",
		"get_git_diff",
		"create_commit",
		"run_tests",
		"Role Instructions (Implementer)",
		"Rules",
	}

	for _, section := range requiredSections {
		if !contains(prompt, section) {
			t.Errorf("prompt missing section: %q", section)
		}
	}
}

func TestBuildSystemPrompt_WithSpec(t *testing.T) {
	spec := []byte(`{"overview": "Implement OAuth2 login", "details": "Use GitHub OAuth"}`)
	task := &models.Task{
		ID:           "task-1",
		ProjectID:    "proj-1",
		RepositoryID: "repo-1",
		CreatedBy:    "user-1",
		Source:       "web",
		Title:        "Add OAuth",
		Status:       models.TaskStatusApproved,
		Priority:     models.PriorityHigh,
		RiskLevel:    models.RiskLevelLow,
		TargetBranch: "main",
		Spec:         spec,
	}

	ws := &models.Workspace{
		ID:           "ws-1",
		RepositoryID: "repo-1",
		Name:         "test-ws",
		Branch:       "feature/oauth",
		Status:       models.WorkspaceStatusReady,
	}

	prompt := BuildSystemPrompt("implementer", task, ws)

	if !contains(prompt, "## Spec") {
		t.Error("prompt missing Spec section")
	}
	if !contains(prompt, "Implement OAuth2 login") {
		t.Error("prompt missing spec overview content")
	}
}

func TestBuildSystemPrompt_WithAcceptanceCriteria(t *testing.T) {
	criteria := []byte(`["User can login with GitHub", "Session is persisted"]`)
	task := &models.Task{
		ID:                 "task-1",
		ProjectID:          "proj-1",
		RepositoryID:       "repo-1",
		CreatedBy:          "user-1",
		Source:             "web",
		Title:              "Add OAuth",
		Status:             models.TaskStatusApproved,
		Priority:           models.PriorityHigh,
		RiskLevel:          models.RiskLevelLow,
		TargetBranch:       "main",
		AcceptanceCriteria: criteria,
	}

	ws := &models.Workspace{
		ID:           "ws-1",
		RepositoryID: "repo-1",
		Name:         "test-ws",
		Branch:       "feature/oauth",
		Status:       models.WorkspaceStatusReady,
	}

	prompt := BuildSystemPrompt("implementer", task, ws)

	if !contains(prompt, "## Acceptance Criteria") {
		t.Error("prompt missing Acceptance Criteria section")
	}
	if !contains(prompt, "User can login with GitHub") {
		t.Error("prompt missing criteria content")
	}
}

func TestBuildSystemPrompt_DifferentRoles(t *testing.T) {
	task := &models.Task{
		ID:           "task-1",
		ProjectID:    "proj-1",
		RepositoryID: "repo-1",
		CreatedBy:    "user-1",
		Source:       "web",
		Title:        "Test task",
		Status:       models.TaskStatusApproved,
		Priority:     models.PriorityMedium,
		RiskLevel:    models.RiskLevelLow,
		TargetBranch: "main",
	}

	ws := &models.Workspace{
		ID:           "ws-1",
		RepositoryID: "repo-1",
		Name:         "test-ws",
		Branch:       "main",
		Status:       models.WorkspaceStatusReady,
	}

	roles := []string{
		models.AgentRoleImplementer,
		models.AgentRolePlanner,
		models.AgentRoleReviewer,
		models.AgentRoleTestRunner,
		models.AgentRoleSecurity,
		models.AgentRoleDocs,
		models.AgentRoleReleaseManager,
	}

	for _, role := range roles {
		prompt := BuildSystemPrompt(role, task, ws)
		if prompt == "" {
			t.Errorf("BuildSystemPrompt(%q) returned empty string", role)
		}
		if !contains(prompt, role) {
			t.Errorf("BuildSystemPrompt(%q) doesn't mention role name", role)
		}
	}
}

func TestBuildToolCallPrompt(t *testing.T) {
	history := []models.AgentStep{
		{
			ID:         "step-1",
			AgentRunID: "run-1",
			StepNumber: 1,
			StepType:   models.AgentStepTypeToolCall,
			Status:     models.AgentStepStatusCompleted,
		},
	}

	task := &models.Task{
		ID:           "task-1",
		ProjectID:    "proj-1",
		RepositoryID: "repo-1",
		CreatedBy:    "user-1",
		Source:       "web",
		Title:        "Test task",
		Status:       models.TaskStatusApproved,
		Priority:     models.PriorityMedium,
		RiskLevel:    models.RiskLevelLow,
		TargetBranch: "main",
	}

	prompt := BuildToolCallPrompt(nil, history, nil, task)

	if prompt == "" {
		t.Fatal("BuildToolCallPrompt returned empty string")
	}

	requiredSections := []string{
		"Previous Steps",
		"Mailbox Messages",
		"Instructions",
		"tool_call",
		"final_response",
		"handoff",
	}

	for _, section := range requiredSections {
		if !contains(prompt, section) {
			t.Errorf("prompt missing section: %q", section)
		}
	}
}

func TestBuildToolCallPrompt_EmptyHistory(t *testing.T) {
	task := &models.Task{
		ID:           "task-1",
		ProjectID:    "proj-1",
		RepositoryID: "repo-1",
		CreatedBy:    "user-1",
		Source:       "web",
		Title:        "Test task",
		Status:       models.TaskStatusApproved,
		Priority:     models.PriorityMedium,
		RiskLevel:    models.RiskLevelLow,
		TargetBranch: "main",
	}

	prompt := BuildToolCallPrompt(nil, nil, nil, task)

	if !contains(prompt, "No previous steps") {
		t.Error("expected 'No previous steps' in prompt")
	}
}

func TestFormatMap(t *testing.T) {
	m := map[string]any{
		"name": "test",
		"nested": map[string]any{
			"key": "value",
		},
		"items": []any{"a", "b"},
	}

	result := formatMap(m, 0)

	if result == "" {
		t.Fatal("formatMap returned empty string")
	}
	if !contains(result, "name:") {
		t.Error("expected 'name:' in output")
	}
	if !contains(result, "nested:") {
		t.Error("expected 'nested:' in output")
	}
	if !contains(result, "items:") {
		t.Error("expected 'items:' in output")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsInternal(s, substr))
}

func containsInternal(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
