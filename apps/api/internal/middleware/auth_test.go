package middleware

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/api/internal/auth"
)

func TestExtractBearer(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"valid", "Bearer token123", "token123"},
		{"lowercase", "bearer token123", "token123"},
		{"missing", "", ""},
		{"no bearer", "Basic dXNlcjpwYXNz", ""},
		{"single part", "Bearertoken", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			if got := extractBearer(req); got != tt.want {
				t.Errorf("extractBearer = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuth_MissingToken(t *testing.T) {
	mw := Auth("secret", nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "unauthorized: missing token" {
		t.Errorf("error = %q", body["error"])
	}
}

func TestAuth_InvalidToken(t *testing.T) {
	mw := Auth("secret", nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "invalid token" {
		t.Errorf("error = %q", body["error"])
	}
}

func TestAuth_ValidToken(t *testing.T) {
	secret := "test-secret"
	token, err := auth.GenerateToken("user-1", "org-1", "user@example.com", "admin", secret, time.Hour)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	var captured *auth.Claims
	mw := Auth(secret, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.UserFromContext(r.Context())
		if claims == nil {
			t.Error("expected claims in context")
		}
		captured = claims
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if captured == nil {
		t.Fatal("expected captured claims")
	}
	if captured.UserID != "user-1" {
		t.Errorf("user_id = %q", captured.UserID)
	}
}

func TestAuthWithMembershipRefreshesRole(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	token, err := auth.GenerateToken("u-1", "org-1", "user@example.com", "owner", "test-secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT role FROM users").
		WithArgs("u-1", "org-1").
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("member"))
	handler := AuthWithMembership("test-secret", nil, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := auth.UserFromContext(r.Context())
		if claims == nil || claims.Role != "member" {
			t.Errorf("effective role must reflect current DB membership: %+v", claims)
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthWithMembershipRejectsRevokedOrMissingMembership(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	token, err := auth.GenerateToken("u-1", "org-1", "user@example.com", "owner", "test-secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT role FROM users").
		WithArgs("u-1", "org-1").WillReturnError(sql.ErrNoRows)
	handler := AuthWithMembership("test-secret", nil, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("revoked user must not reach handler")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthWithMembershipFailsClosedOnDatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	token, err := auth.GenerateToken("u-1", "org-1", "user@example.com", "owner", "test-secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT role FROM users").
		WithArgs("u-1", "org-1").WillReturnError(errors.New("connection lost"))
	handler := AuthWithMembership("test-secret", nil, db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("database failure must not reach handler")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
