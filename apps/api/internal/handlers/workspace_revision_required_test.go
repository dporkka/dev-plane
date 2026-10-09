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

func TestWorkspaceHTTPWriteRequiresExpectedRevision(t *testing.T) {
 for _, scenario := range []struct {
   name string
   path string
   initial string
 }{
   {"existing file", "README.md", "# Hello"},
   {"new file", "new.ts", ""},
 } {
   t.Run(scenario.name, func(t *testing.T) {
     h, mock, cleanup := setupTest(t)
     defer cleanup()
     root, closeWorkspace := setupWorkspace(t)
     defer closeWorkspace()
     id := "ws-required-revision"
     expectAuthorizeWorkspace(mock, id)
     mock.ExpectQuery("SELECT worktree_path FROM workspaces").
       WithArgs(id).WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(root))
     payload, err := json.Marshal(WriteFileRequest{
       Path: scenario.path, Content: "unguarded overwrite",
     })
     if err != nil { t.Fatal(err) }
     req := httptest.NewRequest(http.MethodPost,
       "/workspaces/"+id+"/files/write", bytes.NewReader(payload))
     route := chi.NewRouteContext()
     route.URLParams.Add("id", id)
     req = req.WithContext(withTestUser(context.WithValue(req.Context(), chi.RouteCtxKey, route)))
     rec := httptest.NewRecorder()
     h.WriteWorkspaceFile(rec, req)
     if rec.Code != http.StatusPreconditionRequired {
       t.Fatalf("missing expected_revision: status=%d body=%s", rec.Code, rec.Body.String())
     }
     bytesAfter, err := os.ReadFile(filepath.Join(root, scenario.path))
     if scenario.initial == "" {
       if !os.IsNotExist(err) { t.Fatalf("new file unexpectedly created: %v", err) }
     } else if err != nil || string(bytesAfter) != scenario.initial {
       t.Fatalf("existing file overwritten: bytes=%q err=%v", bytesAfter, err)
     }
     if err := mock.ExpectationsWereMet(); err != nil { t.Fatal(err) }
   })
 }
}
