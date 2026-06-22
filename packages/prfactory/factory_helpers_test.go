package prfactory

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
)

func ptr(s string) *string {
	return &s
}

func TestBuildPRBody(t *testing.T) {
	task := &models.Task{
		ID:          "task-1",
		Title:       "Add feature",
		Description: ptr("Implement the feature"),
	}
	run := &models.AgentRun{
		ID:               "run-1",
		Summary:          ptr("Implemented feature"),
		Model:            ptr("gpt-4"),
		Provider:         ptr("openai"),
		TotalCost:        0.1234,
		PromptTokens:     100,
		CompletionTokens: 50,
	}
	report := &reviewer.ReviewReport{
		Summary:       "Looks good",
		RiskLevel:     "low",
		Approvable:    true,
		TestCoverage:  "80%",
		SecurityNotes: "No issues",
		DiffSummary: reviewer.DiffSummary{
			FilesChanged: 1,
			Insertions:   10,
			Deletions:    2,
			Files: []reviewer.FileChange{
				{Path: "main.go", Status: "modified", Insertions: 10, Deletions: 2},
			},
		},
		Findings: []reviewer.Finding{
			{Severity: "low", Category: "style", Message: "minor issue"},
		},
	}

	factory := NewFactory(nil, nil)
	body := factory.BuildPRBody(task, nil, report, run)

	if !strings.Contains(body, "## Task") {
		t.Error("body missing task section")
	}
	if !strings.Contains(body, "Add feature") {
		t.Error("body missing task title")
	}
	if !strings.Contains(body, "## Implementation Summary") {
		t.Error("body missing implementation summary")
	}
	if !strings.Contains(body, "gpt-4") {
		t.Error("body missing model")
	}
	if !strings.Contains(body, "openai") {
		t.Error("body missing provider")
	}
	if !strings.Contains(body, "$0.1234") {
		t.Error("body missing cost")
	}
	if !strings.Contains(body, "main.go") {
		t.Error("body missing file changes")
	}
	if !strings.Contains(body, "Looks good") {
		t.Error("body missing review summary")
	}
}

func TestBuildPRBody_LongTitle(t *testing.T) {
	task := &models.Task{
		ID:    "task-1",
		Title: strings.Repeat("a", 300),
	}
	run := &models.AgentRun{ID: "run-1"}
	report := &reviewer.ReviewReport{RiskLevel: "low", Approvable: true, SecurityNotes: "ok"}

	factory := NewFactory(nil, nil)
	body := factory.BuildPRBody(task, nil, report, run)

	if !strings.HasPrefix(body, "## Task") {
		t.Error("expected body to start with task section")
	}
}

func TestBuildPRBody_HighRisk(t *testing.T) {
	task := &models.Task{ID: "task-1", Title: "Risky change"}
	run := &models.AgentRun{ID: "run-1"}
	report := &reviewer.ReviewReport{
		RiskLevel:     "high",
		Approvable:    false,
		SecurityNotes: "issues found",
		Findings: []reviewer.Finding{
			{Severity: "high", Category: "security", Message: "vulnerability"},
		},
	}

	factory := NewFactory(nil, nil)
	body := factory.BuildPRBody(task, nil, report, run)

	if !strings.Contains(body, "**Risk Level: HIGH**") {
		t.Errorf("body missing high risk warning: %s", body)
	}
}

func TestConfigureGitAskPass(t *testing.T) {
	cmd := &exec.Cmd{}
	cleanup, err := configureGitAskPass(cmd, "secret-token")
	if err != nil {
		t.Fatalf("configureGitAskPass: %v", err)
	}
	defer cleanup()

	script := getEnv(cmd, "GIT_ASKPASS")
	if script == "" {
		t.Fatal("GIT_ASKPASS not set")
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("askpass script missing: %v", err)
	}
	if getEnv(cmd, "GITHUB_TOKEN") != "secret-token" {
		t.Errorf("GITHUB_TOKEN = %q, want secret-token", getEnv(cmd, "GITHUB_TOKEN"))
	}

	content, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if !strings.Contains(string(content), "x-access-token") {
		t.Errorf("script missing username helper: %s", content)
	}
}

func TestConfigureGitAskPass_NoToken(t *testing.T) {
	cmd := &exec.Cmd{}
	cleanup, err := configureGitAskPass(cmd, "")
	if err != nil {
		t.Fatalf("configureGitAskPass: %v", err)
	}
	defer cleanup()

	if getEnv(cmd, "GIT_ASKPASS") != "" {
		t.Error("expected no GIT_ASKPASS when token is empty")
	}
}

func TestNewFactory_ReadsGitHubToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", " env-token ")
	t.Setenv("GITHUB_CLIENT_ID", "client-id")
	t.Setenv("GITHUB_CLIENT_SECRET", "client-secret")

	factory := NewFactory(nil, nil)
	if factory.githubToken != "env-token" {
		t.Errorf("token = %q, want env-token", factory.githubToken)
	}
	if factory.github == nil {
		t.Error("expected github gateway to be configured")
	}
}

func TestNewFactory_NoToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	factory := NewFactory(nil, nil)
	if factory.githubToken != "" {
		t.Errorf("token = %q, want empty", factory.githubToken)
	}
	if factory.github != nil {
		t.Error("expected no github gateway without token")
	}
}

func TestWithGitHubToken(t *testing.T) {
	factory := NewFactory(nil, nil).WithGitHubToken(" token ")
	if factory.githubToken != "token" {
		t.Errorf("token = %q, want token", factory.githubToken)
	}
}

func TestWithGitHubGateway(t *testing.T) {
	factory := NewFactory(nil, nil)
	gh := gateway.NewGitHubGateway("id", "secret")
	factory.WithGitHubGateway(gh)
	if factory.github == nil {
		t.Error("expected github gateway to be set")
	}
}

func TestGetRepoOwnerName(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT full_name FROM repositories").
		WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"full_name"}).AddRow("acme/app"))

	factory := NewFactory(db, nil)
	owner, name, err := factory.getRepoOwnerName(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("getRepoOwnerName: %v", err)
	}
	if owner != "acme" || name != "app" {
		t.Errorf("owner/name = %s/%s, want acme/app", owner, name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetRepoOwnerName_InvalidFullName(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT full_name FROM repositories").
		WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"full_name"}).AddRow("invalid"))

	factory := NewFactory(db, nil)
	_, _, err = factory.getRepoOwnerName(context.Background(), "repo-1")
	if err == nil {
		t.Fatal("expected error for invalid full_name")
	}
	if !strings.Contains(err.Error(), "invalid repository full_name") {
		t.Errorf("error = %v", err)
	}
}

func TestGetRepoOwnerName_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT full_name FROM repositories").
		WithArgs("repo-1").
		WillReturnError(sqlmock.ErrCancelled)

	factory := NewFactory(db, nil)
	_, _, err = factory.getRepoOwnerName(context.Background(), "repo-1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func getEnv(cmd *exec.Cmd, key string) string {
	prefix := key + "="
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return ""
}
