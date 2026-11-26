package handlers

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// These tests specify the security boundary: a mutable GitHub login must not
// select a tenant or elevate a previously provisioned user.
func TestGitHubOAuthExistingPrincipalUsesStoredMembership(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").WithArgs("42").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}).
			AddRow("user-1", "original-tenant", "member", nil, nil))
	mock.ExpectCommit()

	principal, err := resolveGitHubOAuthPrincipal(context.Background(), db, githubOAuthProfile{
		ID: 42, Login: "renamed-owner", Email: "user@example.com", Name: "User",
	})
	if err != nil {
		t.Fatalf("resolve existing principal: %v", err)
	}
	if principal.UserID != "user-1" || principal.OrgID != "original-tenant" || principal.Role != "member" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubOAuthRejectsDeletedAccountWithoutReprovision(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").WithArgs("42").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}).
			AddRow("user-1", "org-1", "owner", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), nil))
	mock.ExpectRollback()

	_, err = resolveGitHubOAuthPrincipal(context.Background(), db, githubOAuthProfile{
		ID: 42, Login: "reused-name", Email: "user@example.com",
	})
	if err == nil {
		t.Fatal("disabled account must not be silently provisioned again")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubOAuthRejectsAmbiguousIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").WithArgs("42").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}).
			AddRow("user-1", "org-1", "owner", nil, nil).
			AddRow("user-2", "org-2", "owner", nil, nil))
	mock.ExpectRollback()

	_, err = resolveGitHubOAuthPrincipal(context.Background(), db, githubOAuthProfile{
		ID: 42, Login: "any", Email: "user@example.com",
	})
	if err == nil {
		t.Fatal("duplicate provider subjects must fail closed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubOAuthRejectsInvalidStoredRole(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").WithArgs("42").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}).
			AddRow("user-1", "org-1", "root", nil, nil))
	mock.ExpectRollback()
	_, err = resolveGitHubOAuthPrincipal(context.Background(), db, githubOAuthProfile{
		ID: 42, Login: "example", Email: "user@example.com",
	})
	if err == nil {
		t.Fatal("unknown roles must fail closed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubOAuthFreshIdentityUsesImmutableSubjectSlug(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").WithArgs("42").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}))
	mock.ExpectExec("INSERT INTO organizations").
		WithArgs(sqlmock.AnyArg(), "User", "github-user-42", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO users").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	principal, err := resolveGitHubOAuthPrincipal(context.Background(), db, githubOAuthProfile{
		ID: 42, Login: "mutable-login", Email: "user@example.com", Name: "User",
	})
	if err != nil {
		t.Fatalf("provision new principal: %v", err)
	}
	if principal.OrgID == "" || principal.UserID == "" || principal.Role != "owner" {
		t.Fatalf("unexpected new principal: %+v", principal)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubOAuthDoesNotIssuePrincipalOnProvisionFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").WithArgs("42").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}))
	mock.ExpectExec("INSERT INTO organizations").
		WillReturnError(errors.New("database unavailable"))
	mock.ExpectRollback()
	_, err = resolveGitHubOAuthPrincipal(context.Background(), db, githubOAuthProfile{
		ID: 42, Login: "example", Email: "user@example.com",
	})
	if err == nil {
		t.Fatal("failed provisioning must never mint credentials")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubOAuthRejectsMissingProviderSubject(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = resolveGitHubOAuthPrincipal(context.Background(), db, githubOAuthProfile{
		Login: "example", Email: "user@example.com",
	})
	if err == nil {
		t.Fatal("missing immutable provider ID must be rejected")
	}
}

var _ *sql.DB

func TestGitHubOAuthConcurrentSubjectUsesWinningMembership(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	columns := []string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}
	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").WithArgs("42").WillReturnRows(sqlmock.NewRows(columns))
	mock.ExpectExec("INSERT INTO organizations").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO organizations").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").WithArgs("42").
		WillReturnRows(sqlmock.NewRows(columns).AddRow("winner", "winner-org", "member", nil, nil))
	mock.ExpectCommit()

	got, err := resolveGitHubOAuthPrincipal(context.Background(), db, githubOAuthProfile{
		ID: 42, Login: "example", Email: "user@example.com",
	})
	if want := (githubOAuthPrincipal{UserID: "winner", OrgID: "winner-org", Role: "member"}); err != nil || got != want {
		t.Fatalf("principal = %+v, %v; want %+v", got, err, want)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
