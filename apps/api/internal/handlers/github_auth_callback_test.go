package handlers

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/config"
	"golang.org/x/oauth2"
)

type githubTestTransport struct {
	host string
	base http.RoundTripper
}

func (t githubTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copyReq := r.Clone(r.Context())
	if copyReq.URL.Host == "api.github.com" {
		u, _ := url.Parse(t.host)
		copyReq.URL.Scheme = u.Scheme
		copyReq.URL.Host = u.Host
	}
	return t.base.RoundTrip(copyReq)
}

func TestGitHubCallbackExistingUserKeepsStoredOrganization(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"gho-test","token_type":"bearer"}`))
		case "/user":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":1234,"login":"new-github-login","email":"user@example.com","name":"Example"}`))
		default:
			t.Errorf("unexpected GitHub endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer svc.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").
		WithArgs("1234").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}).AddRow("user-1", "org-from-db", "member", nil, nil))
	mock.ExpectCommit()
	cfg := &config.Config{GitHubClientID: "test-client", GitHubSecret: "test-secret", JWTSecret: "01234567890123456789012345678901"}
	h := NewGitHubAuthHandler(db, cfg)
	h.oauth.Endpoint.TokenURL = svc.URL + "/token"
	client := &http.Client{Transport: githubTestTransport{host: svc.URL, base: http.DefaultTransport}}
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=test-state&code=some-code", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "test-state"})
	req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, client))
	rec := httptest.NewRecorder()
	h.GitHubAuthCallback(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	claims, err := auth.ValidateToken(body.Token, cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if claims.OrgID != "org-from-db" || claims.UserID != "user-1" || claims.Role != "member" {
		t.Fatalf("token identity mismatch: org=%q user=%q role=%q", claims.OrgID, claims.UserID, claims.Role)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubCallbackNewIdentityCannotClaimExistingSlug(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"access_token":"gho-test","token_type":"bearer"}`))
		case "/user":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":1234,"login":"new-github-login","email":"user@example.com","name":"Example"}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer svc.Close()
	mock.ExpectBegin()
	mock.ExpectQuery("FROM users u").
		WithArgs("1234").
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "role", "deleted_at", "org_deleted_at"}))
	mock.ExpectExec("INSERT INTO organizations").
		WithArgs(sqlmock.AnyArg(), "Example", "github-user-1234", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO organizations").
		WithArgs(sqlmock.AnyArg(), "Example", githubFallbackSlug{}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO users").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), "user@example.com", "Example", "", "1234", "new-github-login", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	cfg := &config.Config{GitHubClientID: "test-client", GitHubSecret: "test-secret", JWTSecret: "01234567890123456789012345678901"}
	h := NewGitHubAuthHandler(db, cfg)
	h.oauth.Endpoint.TokenURL = svc.URL + "/token"
	client := &http.Client{Transport: githubTestTransport{host: svc.URL, base: http.DefaultTransport}}
	req := httptest.NewRequest(http.MethodGet, "/auth/github/callback?state=state&code=code", nil)
	req.AddCookie(&http.Cookie{Name: "oauth_state", Value: "state"})
	req = req.WithContext(context.WithValue(req.Context(), oauth2.HTTPClient, client))
	rec := httptest.NewRecorder()
	h.GitHubAuthCallback(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	claims, err := auth.ValidateToken(body.Token, cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	if claims.OrgID == "" || claims.OrgID == "existing-foreign-org" {
		t.Fatalf("new GitHub identity must not claim preexisting organization: %q", claims.OrgID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

type githubFallbackSlug struct{}

func (githubFallbackSlug) Match(value driver.Value) bool {
	slug, ok := value.(string)
	return ok && strings.HasPrefix(slug, "github-user-1234-") && len(slug) > len("github-user-1234-")
}
