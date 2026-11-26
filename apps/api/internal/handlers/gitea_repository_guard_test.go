package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
)

func TestLegacySyncAndAnalysisRejectGitea(t *testing.T) {
	for _, route := range []string{"sync", "analysis"} {
		t.Run(route, func(t *testing.T) {
			h, mock, cleanup := setupTest(t)
			defer cleanup()
			expectAuthorizeRepository(mock, "repo-1")
			settings := `{"forge":{"provider":"gitea","base_url":"https://git.example.test"}}`
			if route == "sync" {
				mock.ExpectQuery("SELECT settings FROM repositories").WithArgs("repo-1").
					WillReturnRows(sqlmock.NewRows([]string{"settings"}).AddRow(settings))
			} else {
				mock.ExpectQuery("SELECT id, clone_url, full_name, owner, name, settings").WithArgs("repo-1").
					WillReturnRows(sqlmock.NewRows([]string{"id", "clone_url", "full_name", "owner", "name", "settings"}).
						AddRow("repo-1", "https://git.example.test/acme/widget.git", "acme/widget", "acme", "widget", settings))
			}
			req := httptest.NewRequest(http.MethodPost, "/repositories/repo-1/"+route, nil)
			req = req.WithContext(withTestUser(req.Context()))
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "repo-1")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()
			if route == "sync" {
				h.SyncRepository(rec, req)
			} else {
				h.AnalyzeRepo(rec, req)
			}
			if rec.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
