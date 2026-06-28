package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetWorkspaceDiff(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()

	baseDir := t.TempDir()
	t.Setenv("WORKSPACE_BASE_DIR", baseDir)

	workspaceID := "ws-1"
	workspacePath := filepath.Join(baseDir, workspaceID)
	if err := os.MkdirAll(workspacePath, 0755); err != nil {
		t.Fatalf("failed to create workspace dir: %v", err)
	}

	// Initialize git repo, commit a file, then modify it so there is a diff.
	cmd := exec.Command("git", "init")
	cmd.Dir = workspacePath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}
	exec.Command("git", "-C", workspacePath, "config", "user.email", "test@test.com").Run()
	exec.Command("git", "-C", workspacePath, "config", "user.name", "Test").Run()
	if err := os.WriteFile(filepath.Join(workspacePath, "file.txt"), []byte("hello\n"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	exec.Command("git", "-C", workspacePath, "add", ".").Run()
	exec.Command("git", "-C", workspacePath, "commit", "-m", "initial").Run()
	if err := os.WriteFile(filepath.Join(workspacePath, "file.txt"), []byte("world\n"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	expectAuthorizeWorkspace(mock, workspaceID)
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(workspacePath))

	req := workspaceRequest(http.MethodGet, "/workspaces/"+workspaceID+"/diff", workspaceID, nil)
	rec := httptest.NewRecorder()

	h.GetWorkspaceDiff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !strings.Contains(resp["diff"], "file.txt") {
		t.Errorf("expected diff to contain 'file.txt', got %q", resp["diff"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetWorkspaceDiff_EmptyPath(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()

	workspaceID := "ws-empty"
	expectAuthorizeWorkspace(mock, workspaceID)
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(nil))

	req := workspaceRequest(http.MethodGet, "/workspaces/"+workspaceID+"/diff", workspaceID, nil)
	rec := httptest.NewRecorder()

	h.GetWorkspaceDiff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["diff"] != "" {
		t.Errorf("expected empty diff, got %q", resp["diff"])
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetWorkspaceDiff_MaliciousPath(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()

	baseDir := t.TempDir()
	t.Setenv("WORKSPACE_BASE_DIR", baseDir)

	// Path outside the configured workspace base directory.
	outsideDir := t.TempDir()
	workspaceID := "ws-malicious"
	expectAuthorizeWorkspace(mock, workspaceID)
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(outsideDir))

	req := workspaceRequest(http.MethodGet, "/workspaces/"+workspaceID+"/diff", workspaceID, nil)
	rec := httptest.NewRecorder()

	h.GetWorkspaceDiff(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetWorkspaceDiff_SymlinkEscape(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()

	baseDir := t.TempDir()
	t.Setenv("WORKSPACE_BASE_DIR", baseDir)

	outsideDir := t.TempDir()
	workspaceID := "ws-symlink"
	symlinkPath := filepath.Join(baseDir, workspaceID)
	if err := os.Symlink(outsideDir, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	expectAuthorizeWorkspace(mock, workspaceID)
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow(symlinkPath))

	req := workspaceRequest(http.MethodGet, "/workspaces/"+workspaceID+"/diff", workspaceID, nil)
	rec := httptest.NewRecorder()

	h.GetWorkspaceDiff(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGetWorkspaceDiff_RootPath(t *testing.T) {
	h, mock, cleanupDB := setupTest(t)
	defer cleanupDB()

	baseDir := t.TempDir()
	t.Setenv("WORKSPACE_BASE_DIR", baseDir)

	workspaceID := "ws-root"
	expectAuthorizeWorkspace(mock, workspaceID)
	mock.ExpectQuery("SELECT worktree_path FROM workspaces").
		WithArgs(workspaceID).
		WillReturnRows(sqlmock.NewRows([]string{"worktree_path"}).AddRow("/"))

	req := workspaceRequest(http.MethodGet, "/workspaces/"+workspaceID+"/diff", workspaceID, nil)
	rec := httptest.NewRecorder()

	h.GetWorkspaceDiff(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
