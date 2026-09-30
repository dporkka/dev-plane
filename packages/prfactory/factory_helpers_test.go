package prfactory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/forge"
	"github.com/ai-dev-control-plane/gateway"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/reviewer"
	"github.com/ai-dev-control-plane/vcs"
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

type fakeBranchPublisher struct {
	request vcs.PublishRequest
	err     error
}

func (f *fakeBranchPublisher) Publish(_ context.Context, req vcs.PublishRequest) error {
	f.request = req
	return f.err
}

func TestPublishBranchDelegatesToConfiguredPublisher(t *testing.T) {
	publisher := &fakeBranchPublisher{}
	factory := NewFactory(nil, nil).
		WithBranchPublisher(publisher).
		WithBranchRemote("upstream")

	if err := factory.publishBranch(context.Background(), "/tmp/workspace", "agent/task-42"); err != nil {
		t.Fatalf("publishBranch: %v", err)
	}

	if publisher.request.WorkspacePath != "/tmp/workspace" {
		t.Fatalf("workspace = %q", publisher.request.WorkspacePath)
	}
	if publisher.request.Ref != "agent/task-42" {
		t.Fatalf("ref = %q", publisher.request.Ref)
	}
	if publisher.request.Remote != "upstream" {
		t.Fatalf("remote = %q, want upstream", publisher.request.Remote)
	}
}

func TestPublishBranchRequiresPublisher(t *testing.T) {
	factory := NewFactory(nil, nil)
	factory.branchPublisher = nil

	err := factory.publishBranch(context.Background(), "/tmp/workspace", "agent/task-42")
	if err == nil || !strings.Contains(err.Error(), "branch publisher") {
		t.Fatalf("error = %v, want branch publisher validation", err)
	}
}

func TestWithBranchRemoteRejectsBlankByKeepingDefault(t *testing.T) {
	factory := NewFactory(nil, nil).WithBranchRemote("   ")
	if factory.branchRemote != "origin" {
		t.Fatalf("branch remote = %q, want origin", factory.branchRemote)
	}
}

func TestNewFactory_ReadsGitHubAsDefaultForgeConfig(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", " env-token ")
	t.Setenv("GITHUB_CLIENT_ID", "client-id")
	t.Setenv("GITHUB_CLIENT_SECRET", "client-secret")

	factory := NewFactory(nil, nil)
	if factory.forgeCredential.Token != "env-token" {
		t.Errorf("token = %q, want env-token", factory.forgeCredential.Token)
	}
	if factory.forgeProvider == nil || factory.forgeProvider.Name() != "github" {
		t.Errorf("forge provider = %#v, want github", factory.forgeProvider)
	}
}

func TestNewFactory_NoDefaultForgeWithoutToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	factory := NewFactory(nil, nil)
	if factory.forgeCredential.Token != "" {
		t.Errorf("token = %q, want empty", factory.forgeCredential.Token)
	}
	if factory.forgeProvider != nil {
		t.Error("expected no default forge provider without token")
	}
}

func TestWithForgeCredential(t *testing.T) {
	factory := NewFactory(nil, nil).WithForgeCredential(" token ")
	if factory.forgeCredential.Token != "token" {
		t.Errorf("token = %q, want token", factory.forgeCredential.Token)
	}
}

func TestWithForgeProvider(t *testing.T) {
	factory := NewFactory(nil, nil)
	provider := &fakeForgeProvider{}
	factory.WithForgeProvider(provider)
	if factory.forgeProvider != provider {
		t.Error("expected forge provider to be set")
	}
}

func TestWithGitHubCompatibilityShims(t *testing.T) {
	factory := NewFactory(nil, nil)
	gh := gateway.NewGitHubGateway("id", "secret")
	factory.WithGitHubGateway(gh).WithGitHubToken(" token ")
	if factory.forgeProvider != gh {
		t.Error("expected GitHub gateway to populate forge provider")
	}
	if factory.forgeCredential.Token != "token" {
		t.Errorf("forge token = %q, want token", factory.forgeCredential.Token)
	}
	if factory.branchPublisher == nil {
		t.Error("expected GitHub compatibility token to configure branch publisher")
	}
}

func TestGetRepoNamespaceName(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT full_name FROM repositories").
		WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"full_name"}).AddRow("acme/app"))

	factory := NewFactory(db, nil)
	namespace, name, err := factory.getRepoNamespaceName(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("getRepoNamespaceName: %v", err)
	}
	if namespace != "acme" || name != "app" {
		t.Errorf("namespace/name = %s/%s, want acme/app", namespace, name)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetRepoNamespaceName_NestedNamespace(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT full_name FROM repositories").
		WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"full_name"}).AddRow("acme/platform/app"))

	factory := NewFactory(db, nil)
	namespace, name, err := factory.getRepoNamespaceName(context.Background(), "repo-1")
	if err != nil {
		t.Fatalf("getRepoNamespaceName: %v", err)
	}
	if namespace != "acme/platform" || name != "app" {
		t.Fatalf("namespace/name = %s/%s, want acme/platform/app", namespace, name)
	}
}

func TestGetRepoNamespaceName_InvalidFullName(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT full_name FROM repositories").
		WithArgs("repo-1").
		WillReturnRows(sqlmock.NewRows([]string{"full_name"}).AddRow("invalid"))

	factory := NewFactory(db, nil)
	_, _, err = factory.getRepoNamespaceName(context.Background(), "repo-1")
	if err == nil {
		t.Fatal("expected error for invalid full_name")
	}
	if !errors.Is(err, forge.ErrInvalidRequest) {
		t.Errorf("error = %v, want forge.ErrInvalidRequest", err)
	}
}

func TestGetRepoNamespaceName_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create mock db: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("SELECT full_name FROM repositories").
		WithArgs("repo-1").
		WillReturnError(sqlmock.ErrCancelled)

	factory := NewFactory(db, nil)
	_, _, err = factory.getRepoNamespaceName(context.Background(), "repo-1")
	if err == nil {
		t.Fatal("expected error")
	}
}

