package egresspolicy

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/api/internal/auth"
)

const sessionSQLPattern = "SELECT ar.id, p.organization_id[\\s\\S]+FROM workspaces w[\\s\\S]+WHERE w.runtime_session_id = \\$1"

func TestDatabaseSessionResolverBindsAuthenticatedUserToActiveRunAndWorkspace(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := auth.WithUser(context.Background(), &auth.Claims{UserID: "user-1", OrgID: "tenant-1"})
	ctx = WithAuthorizedRun(ctx, "run-1")
	mock.ExpectQuery(sessionSQLPattern).
		WithArgs("session-1", "run-1", "user-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id"}).AddRow("run-1", "tenant-1"))
	resolver := NewDatabaseSessionResolver(db)
	got, err := resolver.ResolveAuthorizedSession(ctx, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RuntimeSessionID != "session-1" || got.AgentID != "run-1" || got.TenantID != "tenant-1" {
		t.Fatalf("incorrect scope: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseSessionResolverRejectsMissingClaimsAndUnboundRuns(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	resolver := NewDatabaseSessionResolver(db)
	invalid := []context.Context{
		context.Background(),
		WithAuthorizedRun(context.Background(), "run-1"),
		auth.WithUser(context.Background(), &auth.Claims{UserID: "u", OrgID: "t"}),
		WithAuthorizedRun(auth.WithUser(context.Background(), &auth.Claims{UserID: "", OrgID: "tenant-1"}), "run-1"),
		WithAuthorizedRun(auth.WithUser(context.Background(), &auth.Claims{UserID: "user-1", OrgID: ""}), "run-1"),
	}
	for i, ctx := range invalid {
		if _, err := resolver.ResolveAuthorizedSession(ctx, "session-1"); !errors.Is(err, ErrDenied) {
			t.Errorf("case %d bypassed identity gate: %v", i, err)
		}
	}
	if _, err := resolver.ResolveAuthorizedSession(nil, "session-1"); !errors.Is(err, ErrDenied) {
		t.Errorf("nil context: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseSessionResolverDeniesCrossTenantAndFailedOrAbsentRuns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rows  *sqlmock.Rows
		dbErr error
	}{
		{"cross-tenant", sqlmock.NewRows([]string{"id", "organization_id"}).AddRow("run-1", "other-tenant"), nil},
		{"different-run", sqlmock.NewRows([]string{"id", "organization_id"}).AddRow("run-2", "tenant-1"), nil},
		{"no-running-run", sqlmock.NewRows([]string{"id", "organization_id"}), nil},
		{"db-error", nil, sql.ErrConnDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			query := mock.ExpectQuery(sessionSQLPattern).WithArgs("session-1", "run-1", "user-1", "tenant-1")
			if tc.dbErr != nil {
				query.WillReturnError(tc.dbErr)
			} else {
				query.WillReturnRows(tc.rows)
			}
			ctx := WithAuthorizedRun(auth.WithUser(context.Background(), &auth.Claims{UserID: "user-1", OrgID: "tenant-1"}), "run-1")
			_, err = NewDatabaseSessionResolver(db).ResolveAuthorizedSession(ctx, "session-1")
			if !errors.Is(err, ErrDenied) {
				t.Errorf("expected deny: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
