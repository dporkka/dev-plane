package handlers

import (
    "context"
    "database/sql"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/DATA-DOG/go-sqlmock"
    "github.com/go-chi/chi/v5"
)

func requestSchedulingDiagnostics(h *Handler, taskID string, authenticated bool) *httptest.ResponseRecorder {
    req := httptest.NewRequest(http.MethodGet, "/tasks/"+taskID+"/scheduling-diagnostics", nil)
    route := chi.NewRouteContext()
    route.URLParams.Add("id", taskID)
    ctx := context.WithValue(req.Context(), chi.RouteCtxKey, route)
    if authenticated {
        ctx = withTestUser(ctx)
    }
    rec := httptest.NewRecorder()
    h.GetTaskSchedulingDiagnostics(rec, req.WithContext(ctx))
    return rec
}

func TestSchedulingDiagnosticsRejectsUnauthenticated(t *testing.T) {
    h, mock, cleanup := setupTest(t)
    defer cleanup()
    rec := requestSchedulingDiagnostics(h, "task-1", false)
    if rec.Code != http.StatusUnauthorized {
        t.Fatalf("status %d, expected 401", rec.Code)
    }
    if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}

func TestSchedulingDiagnosticsDoesNotExposeUnauthorizedTask(t *testing.T) {
    h, mock, cleanup := setupTest(t)
    defer cleanup()
    mock.ExpectQuery("SELECT").WithArgs("secret-task").WillReturnError(sql.ErrNoRows)
    rec := requestSchedulingDiagnostics(h, "secret-task", true)
    if rec.Code != http.StatusNotFound {
        t.Fatalf("status %d, expected 404", rec.Code)
    }
    if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}

func TestSchedulingDiagnosticsDetectsDependencyAndOwnershipConflictWithoutWrites(t *testing.T) {
    h, mock, cleanup := setupTest(t)
    defer cleanup()
    expectAuthorizeTask(mock, "task-1")
    mock.ExpectQuery("SELECT project_id, repository_id, status, COALESCE\\(metadata").
        WithArgs("task-1").
        WillReturnRows(sqlmock.NewRows([]string{"project_id", "repository_id", "status", "metadata"}).
            AddRow("project-1", "repo-1", "approved", `{"scheduler":{"owns":["apps/api"],"depends_on":["prerequisite"]}}`))
    mock.ExpectQuery("SELECT status FROM tasks").
        WithArgs("prerequisite", "project-1", "repo-1").
        WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("running"))
    mock.ExpectQuery("SELECT t.id, COALESCE\\(t.metadata").
        WithArgs("project-1", "repo-1", "task-1").
        WillReturnRows(sqlmock.NewRows([]string{"id", "metadata"}).
            AddRow("other", `{"scheduler":{"owns":["apps/api/handlers"]}}`))

    rec := requestSchedulingDiagnostics(h, "task-1", true)
    if rec.Code != http.StatusOK { t.Fatalf("status %d: %s", rec.Code, rec.Body.String()) }
    var got SchedulingDiagnostics
    if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil { t.Fatal(err) }
    if got.AdmissionAuthoritative || got.Status != "blocked" {
        t.Fatalf("unexpected admission claim: %+v", got)
    }
    if got.Checks["dependencies"].Status != "blocked" ||
        got.Checks["ownership"].Status != "blocked" ||
        got.Checks["capacity"].Status != "unknown" ||
        got.Checks["budget"].Status != "unknown" {
        t.Fatalf("unexpected checks: %+v", got.Checks)
    }
    if got.ActiveClaims != 1 {
        t.Fatalf("active claims = %d, want 1", got.ActiveClaims)
    }
    // The competing task ID and untrusted dependency metadata must not be echoed.
    if got.Checks["ownership"].Reason != "ownership-conflict" {
        t.Fatalf("ownership reason = %q", got.Checks["ownership"].Reason)
    }
    if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}

func TestSchedulingDiagnosticsFailClosedForOutOfScopeDependency(t *testing.T) {
    h, mock, cleanup := setupTest(t)
    defer cleanup()
    expectAuthorizeTask(mock, "task-1")
    mock.ExpectQuery("SELECT project_id, repository_id, status, COALESCE\\(metadata").
        WithArgs("task-1").
        WillReturnRows(sqlmock.NewRows([]string{"project_id", "repository_id", "status", "metadata"}).
            AddRow("project-1", "repo-1", "approved", `{"scheduler":{"owns":["apps/api"],"depends_on":["foreign"]}}`))
    mock.ExpectQuery("SELECT status FROM tasks").
        WithArgs("foreign", "project-1", "repo-1").
        WillReturnError(sql.ErrNoRows)
    mock.ExpectQuery("SELECT t.id, COALESCE\\(t.metadata").
        WithArgs("project-1", "repo-1", "task-1").
        WillReturnRows(sqlmock.NewRows([]string{"id", "metadata"}))

    rec := requestSchedulingDiagnostics(h, "task-1", true)
    if rec.Code != http.StatusOK { t.Fatalf("status %d: %s", rec.Code, rec.Body.String()) }
    var got SchedulingDiagnostics
    if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil { t.Fatal(err) }
    if got.Status != "blocked" || got.Checks["dependencies"].Reason != "dependency-unavailable" {
        t.Fatalf("unexpected result: %+v", got)
    }
    if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}

func TestSchedulingDiagnosticsDoesNotClaimReadinessForLegacyTask(t *testing.T) {
    h, mock, cleanup := setupTest(t)
    defer cleanup()
    expectAuthorizeTask(mock, "task-1")
    mock.ExpectQuery("SELECT project_id, repository_id, status, COALESCE\\(metadata").
        WithArgs("task-1").
        WillReturnRows(sqlmock.NewRows([]string{"project_id", "repository_id", "status", "metadata"}).
            AddRow("project-1", "repo-1", "approved", "{}"))
    mock.ExpectQuery("SELECT COALESCE\\(files_to_change").
        WithArgs("task-1").
        WillReturnError(sql.ErrNoRows)
    mock.ExpectQuery("SELECT t.id, COALESCE\\(t.metadata").
        WithArgs("project-1", "repo-1", "task-1").
        WillReturnRows(sqlmock.NewRows([]string{"id", "metadata"}))

    rec := requestSchedulingDiagnostics(h, "task-1", true)
    if rec.Code != http.StatusOK { t.Fatalf("status %d: %s", rec.Code, rec.Body.String()) }
    var got SchedulingDiagnostics
    if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil { t.Fatal(err) }
    if got.AdmissionAuthoritative || got.Status != "unknown" ||
       got.Checks["ownership"].Status != "unknown" {
        t.Fatalf("legacy task was incorrectly cleared: %+v", got)
    }
    if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}
