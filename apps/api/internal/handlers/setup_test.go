package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/policies"
)

const (
	testUserID = "user-1"
	testOrgID  = "org-1"
)

func setupTest(t *testing.T) (*Handler, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	allowAll := policies.NewEngine([]policies.Policy{
		{Name: "allow_all_tests", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	})
	h := NewHandler(db, slog.Default()).WithCapabilityKernel(capability.NewKernel(allowAll, nil, nil, slog.Default()))
	cleanup := func() { db.Close() }
	return h, mock, cleanup
}

// testUser returns the standard test user claims used across handler tests.
func testUser() *auth.Claims {
	return &auth.Claims{
		UserID: testUserID,
		OrgID:  testOrgID,
		Email:  "test@example.com",
		Role:   "member",
	}
}

// withTestUser injects the standard test user into the context.
func withTestUser(ctx context.Context) context.Context {
	return auth.WithUser(ctx, testUser())
}

// expectAuthorizeOrganization sets up a sqlmock expectation for organization
// ownership authorization.
func expectAuthorizeOrganization(mock sqlmock.Sqlmock, orgID string) {
	mock.ExpectQuery("SELECT EXISTS").
		WithArgs(testUserID, orgID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
}

// expectAuthorizeByColumn sets up a sqlmock expectation for a direct
// organization_id column lookup on the given table.
func expectAuthorizeByColumn(mock sqlmock.Sqlmock, table, value, orgID string) {
	mock.ExpectQuery(fmt.Sprintf(`(?s)SELECT\s+organization_id\s+FROM\s+%s`, table)).
		WithArgs(value).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(orgID))
}

// expectAuthorizeProject sets up a sqlmock expectation for project ownership.
func expectAuthorizeProject(mock sqlmock.Sqlmock, projectID string) {
	expectAuthorizeByColumn(mock, "projects", projectID, testOrgID)
}

// expectAuthorizeRepository sets up a sqlmock expectation for repository ownership.
func expectAuthorizeRepository(mock sqlmock.Sqlmock, repoID string) {
	mock.ExpectQuery(`(?s)SELECT\s+p\.organization_id\s+FROM\s+repositories\s+r`).
		WithArgs(repoID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(testOrgID))
}

// expectAuthorizeTask sets up a sqlmock expectation for task ownership.
func expectAuthorizeTask(mock sqlmock.Sqlmock, taskID string) {
	mock.ExpectQuery(`(?s)SELECT\s+p\.organization_id\s+FROM\s+tasks\s+t`).
		WithArgs(taskID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(testOrgID))
}

// expectAuthorizeWorkspace sets up a sqlmock expectation for workspace ownership.
func expectAuthorizeWorkspace(mock sqlmock.Sqlmock, workspaceID string) {
	mock.ExpectQuery(`(?s)SELECT\s+p\.organization_id\s+FROM\s+workspaces\s+w`).
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(testOrgID))
}

// expectAuthorizeAgentRun sets up a sqlmock expectation for agent run ownership.
func expectAuthorizeAgentRun(mock sqlmock.Sqlmock, runID string) {
	mock.ExpectQuery(`(?s)SELECT\s+p\.organization_id\s+FROM\s+agent_runs\s+ar`).
		WithArgs(runID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(testOrgID))
}

// expectAuthorizeApproval sets up a sqlmock expectation for approval ownership.
func expectAuthorizeApproval(mock sqlmock.Sqlmock, approvalID string) {
	mock.ExpectQuery(`(?s)SELECT\s+p\.organization_id\s+FROM\s+approvals\s+a`).
		WithArgs(approvalID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(testOrgID))
}

// expectAuthorizePullRequest sets up a sqlmock expectation for pull request ownership.
func expectAuthorizePullRequest(mock sqlmock.Sqlmock, prID string) {
	mock.ExpectQuery(`(?s)SELECT\s+p\.organization_id\s+FROM\s+pull_requests\s+pr`).
		WithArgs(prID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(testOrgID))
}

// expectAuthorizeSecret sets up a sqlmock expectation for secret ownership.
func expectAuthorizeSecret(mock sqlmock.Sqlmock, secretID string) {
	expectAuthorizeByColumn(mock, "secret_references", secretID, testOrgID)
}

// expectAuthorizeIntegration sets up a sqlmock expectation for integration ownership.
func expectAuthorizeIntegration(mock sqlmock.Sqlmock, integrationID string) {
	expectAuthorizeByColumn(mock, "integrations", integrationID, testOrgID)
}

// expectAuthorizePolicy sets up a sqlmock expectation for policy ownership.
func expectAuthorizePolicy(mock sqlmock.Sqlmock, policyID string) {
	expectAuthorizeByColumn(mock, "policies", policyID, testOrgID)
}

// expectAuthorizeArtifact sets up a sqlmock expectation for artifact ownership.
func expectAuthorizeArtifact(mock sqlmock.Sqlmock, artifactID string) {
	expectAuthorizeByColumn(mock, "artifacts", artifactID, testOrgID)
}
