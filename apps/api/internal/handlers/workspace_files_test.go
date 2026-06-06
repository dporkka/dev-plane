package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	_ "github.com/mattn/go-sqlite3"

	"github.com/ai-dev-control-plane/api/internal/audit"
	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
)

func setupWorkspace(t *testing.T) (workspacePath string, cleanup func()) {
	t.Helper()
	workspacePath = t.TempDir()
	// Create some test files
	if err := os.WriteFile(filepath.Join(workspacePath, "README.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workspacePath, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, "subdir", "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatalf("failed to create subdir file: %v", err)
	}
	return workspacePath, func() { os.RemoveAll(workspacePath) }
}

func TestReadWorkspaceFile(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspaceID+"/files?path=README.md", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ReadWorkspaceFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["path"] != "README.md" {
		t.Errorf("expected path 'README.md', got %q", resp["path"])
	}

	if resp["content"] != "# Hello" {
		t.Errorf("expected content '# Hello', got %q", resp["content"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestReadWorkspaceFile_NotFound(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspaceID+"/files?path=nonexistent.txt", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ReadWorkspaceFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestReadWorkspaceFile_Traversal(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"

	traversalPaths := []string{
		"../../../etc/passwd",
		"../outside/file.txt",
		"subdir/../../outside",
	}

	for _, path := range traversalPaths {
		mock.ExpectQuery("SELECT worktree_path FROM workspaces").
			WithArgs(workspaceID).
			WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

		req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspaceID+"/files?path="+path, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", workspaceID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		rec := httptest.NewRecorder()

		h.ReadWorkspaceFile(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("path %q: expected status %d, got %d", path, http.StatusForbidden, rec.Code)
		}

		body := rec.Body.String()
		if !strings.Contains(body, "path traversal") {
			t.Errorf("path %q: expected 'path traversal' in error body, got %q", path, body)
		}
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestWriteWorkspaceFile(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	body, _ := json.Marshal(WriteFileRequest{
		Path:    "newfile.txt",
		Content: "new content",
	})
	req := httptest.NewRequest(http.MethodPut, "/workspaces/"+workspaceID+"/files", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.WriteWorkspaceFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "written" {
		t.Errorf("expected status 'written', got %q", resp["status"])
	}

	if resp["path"] != "newfile.txt" {
		t.Errorf("expected path 'newfile.txt', got %q", resp["path"])
	}

	// Verify file was written
	content, err := os.ReadFile(filepath.Join(workspacePath, "newfile.txt"))
	if err != nil {
		t.Fatalf("failed to read written file: %v", err)
	}
	if string(content) != "new content" {
		t.Errorf("expected file content 'new content', got %q", string(content))
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestWriteWorkspaceFile_Traversal(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"

	// Create a directory outside the workspace to test traversal
	outsideDir := filepath.Join(workspacePath, "..", "outside")
	os.MkdirAll(outsideDir, 0755)
	defer os.RemoveAll(outsideDir)

	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	body, _ := json.Marshal(WriteFileRequest{
		Path:    "../outside/malicious.txt",
		Content: "malicious",
	})
	req := httptest.NewRequest(http.MethodPut, "/workspaces/"+workspaceID+"/files", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.WriteWorkspaceFile(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}

	bodyStr := rec.Body.String()
	if !strings.Contains(bodyStr, "path traversal") {
		t.Errorf("expected 'path traversal' in error body, got %q", bodyStr)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestWriteWorkspaceFile_DefaultPolicyRequiresApproval(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()
	h := NewHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	body, _ := json.Marshal(WriteFileRequest{
		Path:    "blocked.txt",
		Content: "blocked",
	})
	req := httptest.NewRequest(http.MethodPut, "/workspaces/"+workspaceID+"/files", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.WriteWorkspaceFile(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected status %d, got %d", http.StatusLocked, rec.Code)
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected file not to be written, stat err: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestWriteWorkspaceFilePersistsCapabilityAudit(t *testing.T) {
	db := setupWorkspaceAuditDB(t)
	defer db.Close()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, worktree_path, runtime_provider, runtime_session_id, status, deleted_at)
		VALUES ('ws-audit', ?, 'local', NULL, 'ready', NULL)
	`, workspacePath); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	allowAll := policies.NewEngine([]policies.Policy{
		{Name: "allow_all_tests", ResourceType: "*", Action: "*", Effect: policies.EffectAllow},
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewHandler(db, logger).WithCapabilityKernel(capability.NewKernel(allowAll, nil, audit.NewLogger(db, logger), logger))

	body, _ := json.Marshal(WriteFileRequest{Path: "audited.txt", Content: "audited"})
	req := workspaceRequest(http.MethodPost, "/workspaces/ws-audit/files/write", "ws-audit", bytes.NewReader(body))
	req = req.WithContext(auth.WithUser(req.Context(), &auth.Claims{
		UserID: "11111111-1111-1111-1111-111111111111",
		OrgID:  "22222222-2222-2222-2222-222222222222",
		Email:  "user@example.invalid",
		Role:   "admin",
	}))
	rec := httptest.NewRecorder()

	h.WriteWorkspaceFile(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var actorType, orgID, action string
	if err := db.QueryRow(`
		SELECT actor_type, organization_id, action
		FROM audit_logs
		WHERE action = 'capability_check'
	`).Scan(&actorType, &orgID, &action); err != nil {
		t.Fatalf("query audit log: %v", err)
	}
	if actorType != "human" {
		t.Fatalf("actor_type = %q, want human", actorType)
	}
	if orgID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("organization_id = %q", orgID)
	}
	if action != "capability_check" {
		t.Fatalf("action = %q, want capability_check", action)
	}
}

func TestListWorkspaceFiles(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	req := httptest.NewRequest(http.MethodGet, "/workspaces/"+workspaceID+"/files", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ListWorkspaceFiles(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var files []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &files); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Should have README.md and subdir (but not hidden files)
	if len(files) < 1 {
		t.Fatalf("expected at least 1 file, got %d", len(files))
	}

	foundReadme := false
	for _, f := range files {
		if f["name"] == "README.md" {
			foundReadme = true
			break
		}
	}
	if !foundReadme {
		t.Error("expected README.md in file listing")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestExecWorkspaceCommand(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	body, _ := json.Marshal(ExecRequest{
		Command: "echo hello",
		Timeout: 5,
	})
	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+workspaceID+"/exec", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ExecWorkspaceCommand(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["command"] != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", resp["command"])
	}

	stdout, ok := resp["stdout"].(string)
	if !ok {
		t.Fatalf("expected stdout string, got %T", resp["stdout"])
	}

	if !strings.Contains(stdout, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", stdout)
	}

	if resp["exit_code"] != float64(0) {
		t.Errorf("expected exit_code 0, got %v", resp["exit_code"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestExecWorkspaceCommand_Timeout(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	body, _ := json.Marshal(ExecRequest{
		Command: "sleep 10",
		Timeout: 1,
	})
	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+workspaceID+"/exec", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	start := time.Now()
	h.ExecWorkspaceCommand(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status %d, got %d", http.StatusGatewayTimeout, rec.Code)
	}

	// Should have completed quickly, not waited 10 seconds
	if elapsed > 5*time.Second {
		t.Errorf("expected timeout quickly, but took %v", elapsed)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestExecWorkspaceCommand_DefaultPolicyRequiresApproval(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create mock db: %v", err)
	}
	defer db.Close()
	h := NewHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	workspacePath, cleanupWS := setupWorkspace(t)
	defer cleanupWS()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	body, _ := json.Marshal(ExecRequest{
		Command: "touch blocked-exec.txt",
		Timeout: 5,
	})
	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+workspaceID+"/exec", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ExecWorkspaceCommand(rec, req)

	if rec.Code != http.StatusLocked {
		t.Fatalf("expected status %d, got %d", http.StatusLocked, rec.Code)
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "blocked-exec.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected command not to run, stat err: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestApplyWorkspacePatch(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()

	// Create a git repo workspace for patch application
	workspacePath := t.TempDir()
	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = workspacePath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}
	// Configure git user
	exec.Command("git", "-C", workspacePath, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", workspacePath, "config", "user.name", "Test").Run()
	// Create initial file and commit
	os.WriteFile(filepath.Join(workspacePath, "file.txt"), []byte("hello\n"), 0644)
	exec.Command("git", "-C", workspacePath, "add", ".").Run()
	exec.Command("git", "-C", workspacePath, "commit", "-m", "initial").Run()

	workspaceID := "ws-1"
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	// Create a valid git patch
	patch := `diff --git a/file.txt b/file.txt
--- a/file.txt
+++ b/file.txt
@@ -1 +1 @@
-hello
+world
`

	body, _ := json.Marshal(PatchRequest{Patch: patch})
	req := httptest.NewRequest(http.MethodPost, "/workspaces/"+workspaceID+"/patch", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	h.ApplyWorkspacePatch(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp["status"] != "patched" {
		t.Errorf("expected status 'patched', got %q", resp["status"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestStartWorkspaceService(t *testing.T) {
	h, _, cleanup := setupTest(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/workspaces/ws-1/service", nil)
	rec := httptest.NewRecorder()

	h.StartWorkspaceService(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected status %d, got %d", http.StatusNotImplemented, rec.Code)
	}
}

func TestRuntimeWorkspaceFileOperationsUseProvider(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		h, mock, cleanupDB := setupTest(t)
		defer cleanupDB()
		provider := &fakeWorkspaceRuntimeProvider{files: map[string][]byte{"README.md": []byte("# Runtime")}}
		h.WithRuntimeProvider("docker", provider)
		expectDockerRuntimeWorkspace(mock, "ws-runtime")

		req := workspaceRequest(http.MethodGet, "/workspaces/ws-runtime/files/content?path=README.md", "ws-runtime", nil)
		rec := httptest.NewRecorder()
		h.ReadWorkspaceFile(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
		}
		if provider.readSession != "runtime-1" || provider.readPath != "README.md" {
			t.Fatalf("provider read = (%q, %q), want runtime-1 README.md", provider.readSession, provider.readPath)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("write", func(t *testing.T) {
		h, mock, cleanupDB := setupTest(t)
		defer cleanupDB()
		provider := &fakeWorkspaceRuntimeProvider{files: map[string][]byte{}}
		h.WithRuntimeProvider("docker", provider)
		expectDockerRuntimeWorkspace(mock, "ws-runtime")

		body, _ := json.Marshal(WriteFileRequest{Path: "src/app.go", Content: "package main\n"})
		req := workspaceRequest(http.MethodPost, "/workspaces/ws-runtime/files/write", "ws-runtime", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.WriteWorkspaceFile(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
		}
		if string(provider.files["src/app.go"]) != "package main\n" {
			t.Fatalf("provider write content = %q", string(provider.files["src/app.go"]))
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("patch", func(t *testing.T) {
		h, mock, cleanupDB := setupTest(t)
		defer cleanupDB()
		provider := &fakeWorkspaceRuntimeProvider{files: map[string][]byte{}}
		h.WithRuntimeProvider("docker", provider)
		expectDockerRuntimeWorkspace(mock, "ws-runtime")

		body, _ := json.Marshal(PatchRequest{Patch: "diff --git a/a b/a\n"})
		req := workspaceRequest(http.MethodPost, "/workspaces/ws-runtime/patch", "ws-runtime", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.ApplyWorkspacePatch(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
		}
		if provider.patch != "diff --git a/a b/a\n" {
			t.Fatalf("provider patch = %q", provider.patch)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("exec", func(t *testing.T) {
		h, mock, cleanupDB := setupTest(t)
		defer cleanupDB()
		provider := &fakeWorkspaceRuntimeProvider{files: map[string][]byte{}, commandResult: &runtimes.CommandResult{Stdout: "hello\n", ExitCode: 0}}
		h.WithRuntimeProvider("docker", provider)
		expectDockerRuntimeWorkspace(mock, "ws-runtime")

		body, _ := json.Marshal(ExecRequest{Command: "echo hello", Timeout: 5})
		req := workspaceRequest(http.MethodPost, "/workspaces/ws-runtime/exec", "ws-runtime", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		h.ExecWorkspaceCommand(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
		}
		if len(provider.commands) != 1 || provider.commands[0].Command != "echo hello" {
			t.Fatalf("provider commands = %#v", provider.commands)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unfulfilled expectations: %v", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		h, mock, cleanupDB := setupTest(t)
		defer cleanupDB()
		provider := &fakeWorkspaceRuntimeProvider{
			files:         map[string][]byte{},
			commandResult: &runtimes.CommandResult{Stdout: "README.md\tfile\t9\nsrc\tdir\t0\n", ExitCode: 0},
		}
		h.WithRuntimeProvider("docker", provider)
		expectDockerRuntimeWorkspace(mock, "ws-runtime")

		req := workspaceRequest(http.MethodGet, "/workspaces/ws-runtime/files?path=.", "ws-runtime", nil)
		rec := httptest.NewRecorder()
		h.ListWorkspaceFiles(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
		}
		if len(provider.commands) != 1 || provider.commands[0].Dir != "." {
			t.Fatalf("provider commands = %#v", provider.commands)
		}
		if !strings.Contains(rec.Body.String(), "README.md") {
			t.Fatalf("list response missing README.md: %s", rec.Body.String())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unfulfilled expectations: %v", err)
		}
	})
}

func setupWorkspaceAuditDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = db.Exec(`
		CREATE TABLE workspaces (
			id TEXT PRIMARY KEY,
			worktree_path TEXT,
			runtime_provider TEXT,
			runtime_session_id TEXT,
			status TEXT,
			deleted_at DATETIME
		);
		CREATE TABLE audit_logs (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_id TEXT,
			action TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT,
			details TEXT,
			created_at DATETIME
		);
	`)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create audit test schema: %v", err)
	}
	return db
}

func expectDockerRuntimeWorkspace(mock sqlmock.Sqlmock, workspaceID string) {
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(nil))
	mock.ExpectQuery("SELECT id, repository_id, task_id, worktree_path, runtime_provider, runtime_session_id, status").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "repository_id", "task_id", "worktree_path", "runtime_provider", "runtime_session_id", "status",
		}).AddRow(workspaceID, "repo-1", nil, nil, "docker", "runtime-1", "ready"))
}

func workspaceRequest(method, target, workspaceID string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", workspaceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

type fakeWorkspaceRuntimeProvider struct {
	files         map[string][]byte
	readSession   string
	readPath      string
	commands      []runtimes.Command
	commandResult *runtimes.CommandResult
	patch         string
}

func (p *fakeWorkspaceRuntimeProvider) CreateWorkspace(ctx context.Context, req runtimes.CreateRequest) (*runtimes.Session, error) {
	return nil, nil
}

func (p *fakeWorkspaceRuntimeProvider) DestroyWorkspace(ctx context.Context, sessionID string) error {
	return nil
}

func (p *fakeWorkspaceRuntimeProvider) ExecuteCommand(ctx context.Context, sessionID string, cmd runtimes.Command) (*runtimes.CommandResult, error) {
	p.commands = append(p.commands, cmd)
	if p.commandResult != nil {
		return p.commandResult, nil
	}
	return &runtimes.CommandResult{}, nil
}

func (p *fakeWorkspaceRuntimeProvider) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	p.readSession = sessionID
	p.readPath = path
	data, ok := p.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (p *fakeWorkspaceRuntimeProvider) WriteFile(ctx context.Context, sessionID, path string, data []byte) error {
	if p.files == nil {
		p.files = map[string][]byte{}
	}
	p.files[path] = append([]byte(nil), data...)
	return nil
}

func (p *fakeWorkspaceRuntimeProvider) ApplyPatch(ctx context.Context, sessionID, patch string) error {
	p.patch = patch
	return nil
}

func (p *fakeWorkspaceRuntimeProvider) Snapshot(ctx context.Context, sessionID string) (*runtimes.Snapshot, error) {
	return nil, nil
}

func (p *fakeWorkspaceRuntimeProvider) Restore(ctx context.Context, sessionID string, snap *runtimes.Snapshot) error {
	return nil
}

func (p *fakeWorkspaceRuntimeProvider) GetStatus(ctx context.Context, sessionID string) (*runtimes.SessionStatus, error) {
	return &runtimes.SessionStatus{SessionID: sessionID, Status: "ready"}, nil
}

func (p *fakeWorkspaceRuntimeProvider) StreamLogs(ctx context.Context, sessionID string) (<-chan runtimes.LogLine, error) {
	ch := make(chan runtimes.LogLine)
	close(ch)
	return ch, nil
}
