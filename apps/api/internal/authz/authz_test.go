package authz

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/ai-dev-control-plane/api/internal/auth"
)

func TestUserFromRequest(t *testing.T) {
	t.Run("returns user when present", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "u1", OrgID: "o1"}))

		user, err := UserFromRequest(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if user.UserID != "u1" || user.OrgID != "o1" {
			t.Fatalf("expected user u1/o1, got %s/%s", user.UserID, user.OrgID)
		}
	})

	t.Run("errors when missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		_, err := UserFromRequest(req)
		if err == nil {
			t.Fatal("expected error for missing user")
		}
	})
}

func TestRequireUser(t *testing.T) {
	t.Run("returns user and true", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{UserID: "u1", OrgID: "o1"}))
		rec := httptest.NewRecorder()

		user, ok := RequireUser(rec, req)
		if !ok {
			t.Fatal("expected ok=true")
		}
		if user.UserID != "u1" {
			t.Fatalf("expected user u1, got %s", user.UserID)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected recorder unchanged, got %d", rec.Code)
		}
	})

	t.Run("writes 401 when missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		_, ok := RequireUser(rec, req)
		if ok {
			t.Fatal("expected ok=false")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	})
}

func TestAuthorizeOrganization(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	user := &auth.Claims{UserID: "u1", OrgID: "o1"}

	t.Run("allows member", func(t *testing.T) {
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("u1", "o1").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		if err := AuthorizeOrganization(context.Background(), db, user, "o1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("denies non-member", func(t *testing.T) {
		mock.ExpectQuery("SELECT EXISTS").
			WithArgs("u1", "o2").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		err := AuthorizeOrganization(context.Background(), db, user, "o2")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestAuthorizeProject(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	user := &auth.Claims{UserID: "u1", OrgID: "o1"}

	t.Run("allows owning org", func(t *testing.T) {
		mock.ExpectQuery("SELECT organization_id FROM projects").
			WithArgs("p1").
			WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow("o1"))

		if err := AuthorizeProject(context.Background(), db, user, "p1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("denies other org", func(t *testing.T) {
		mock.ExpectQuery("SELECT organization_id FROM projects").
			WithArgs("p2").
			WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow("o2"))

		err := AuthorizeProject(context.Background(), db, user, "p2")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		mock.ExpectQuery("SELECT organization_id FROM projects").
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		err := AuthorizeProject(context.Background(), db, user, "missing")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestAuthorizeTask(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	user := &auth.Claims{UserID: "u1", OrgID: "o1"}

	t.Run("allows owning org", func(t *testing.T) {
		mock.ExpectQuery("SELECT p.organization_id FROM tasks t JOIN projects p").
			WithArgs("t1").
			WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow("o1"))

		if err := AuthorizeTask(context.Background(), db, user, "t1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("denies other org", func(t *testing.T) {
		mock.ExpectQuery("SELECT p.organization_id FROM tasks t JOIN projects p").
			WithArgs("t2").
			WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow("o2"))

		err := AuthorizeTask(context.Background(), db, user, "t2")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestAuthorizeRepository(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()

	user := &auth.Claims{UserID: "u1", OrgID: "o1"}

	t.Run("allows owning org", func(t *testing.T) {
		mock.ExpectQuery("SELECT p.organization_id FROM repositories r JOIN projects p").
			WithArgs("r1").
			WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow("o1"))

		if err := AuthorizeRepository(context.Background(), db, user, "r1"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
