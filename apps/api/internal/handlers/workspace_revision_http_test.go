package handlers

import (
  "bytes"
  "context"
  "encoding/json"
  "net/http"
  "net/http/httptest"
  "os"
  "path/filepath"
  "testing"

  "github.com/DATA-DOG/go-sqlmock"
  "github.com/go-chi/chi/v5"
)

func TestWorkspaceHTTPReadReturnsByteRevision(t *testing.T) {
  h, mock, cleanup := setupTest(t)
  defer cleanup()
  dir, closeDir := setupWorkspace(t)
  defer closeDir()
  workspaceID := "ws-revision-read"
  expectAuthorizeWorkspace(mock, workspaceID)
  mock.ExpectQuery("SELECT worktree_path FROM workspaces").WithArgs(workspaceID).
    WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(dir))
  req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspaceID+"/files/content?path=README.md", nil)
  route := chi.NewRouteContext()
  route.URLParams.Add("id", workspaceID)
  req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, route)))
  rec := httptest.NewRecorder()
  h.ReadWorkspaceFile(rec, req)
  if rec.Code != http.StatusOK { t.Fatalf("status=%d: %s", rec.Code, rec.Body.String()) }
  var response struct { Revision string `json:"revision"` }
  if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil { t.Fatal(err) }
  if response.Revision != workspaceContentRevision([]byte("# Hello")) { t.Fatalf("unexpected revision %q", response.Revision) }
  if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}

func TestWorkspaceHTTPWriteConflictPreservesRemoteFile(t *testing.T) {
  h, mock, cleanup := setupTest(t)
  defer cleanup()
  dir, closeDir := setupWorkspace(t)
  defer closeDir()
  workspaceID := "ws-revision-conflict"
  expectAuthorizeWorkspace(mock, workspaceID)
  mock.ExpectQuery("SELECT worktree_path FROM workspaces").WithArgs(workspaceID).
    WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(dir))
  old := workspaceContentRevision([]byte("old version"))
  body, _ := json.Marshal(WriteFileRequest{
    Path: "README.md", Content: "browser version", ExpectedRevision: &old,
  })
  req := httptest.NewRequest(http.MethodPost, "/workspaces/"+workspaceID+"/files/write", bytes.NewReader(body))
  route := chi.NewRouteContext()
  route.URLParams.Add("id", workspaceID)
  req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, route)))
  rec := httptest.NewRecorder()
  h.WriteWorkspaceFile(rec, req)
  if rec.Code != http.StatusConflict { t.Fatalf("status=%d: %s", rec.Code, rec.Body.String()) }
  content, err := os.ReadFile(filepath.Join(dir, "README.md"))
  if err != nil || string(content) != "# Hello" { t.Fatalf("remote file overwritten: %q (%v)", content, err) }
  if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
}
