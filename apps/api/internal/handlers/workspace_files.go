package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/auth"
	"github.com/ai-dev-control-plane/api/internal/authz"
	"github.com/ai-dev-control-plane/api/internal/capability"
	"github.com/ai-dev-control-plane/api/internal/respond"
	"github.com/ai-dev-control-plane/models"
	"github.com/ai-dev-control-plane/policies"
	"github.com/ai-dev-control-plane/runtimes"
)

// WorkspaceFileHandler handles file operations within a workspace.
// All paths are validated to prevent directory traversal attacks.

// ListFiles lists files in a workspace directory.
func (h *Handler) ListWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	workspaceID := chi.URLParam(r, "id")
	if workspaceID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	workspacePath, err := h.getWorkspacePath(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if workspacePath == "" {
		h.listRuntimeWorkspaceFiles(w, r, workspaceID)
		return
	}

	requestedPath := r.URL.Query().Get("path")
	if err := validateWorkspacePath(workspacePath, requestedPath); err != nil {
		respond.Error(w, http.StatusForbidden, err)
		return
	}

	targetDir := filepath.Join(workspacePath, requestedPath)
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("read directory: %w", err))
		return
	}

	type fileEntry struct {
		Name  string `json:"name"`
		Path  string `json:"path"`
		IsDir bool   `json:"is_dir"`
		Size  int64  `json:"size,omitempty"`
	}

	var files []fileEntry
	for _, entry := range entries {
		// Skip hidden files/dirs
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		fe := fileEntry{
			Name:  entry.Name(),
			Path:  filepath.Join(requestedPath, entry.Name()),
			IsDir: entry.IsDir(),
		}
		if info, err := entry.Info(); err == nil {
			fe.Size = info.Size()
		}
		files = append(files, fe)
	}

	if files == nil {
		files = []fileEntry{}
	}
	respond.JSON(w, http.StatusOK, files)
}

// ReadWorkspaceFile reads the content of a file in a workspace.
func (h *Handler) ReadWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	workspaceID := chi.URLParam(r, "id")
	if workspaceID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	workspacePath, err := h.getWorkspacePath(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if workspacePath == "" {
		h.readRuntimeWorkspaceFile(w, r, workspaceID)
		return
	}

	requestedPath := r.URL.Query().Get("path")
	if requestedPath == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("path query parameter is required"))
		return
	}
	if err := validateWorkspacePath(workspacePath, requestedPath); err != nil {
		respond.Error(w, http.StatusForbidden, err)
		return
	}

	targetFile := filepath.Join(workspacePath, requestedPath)
	info, err := os.Stat(targetFile)
	if err != nil {
		if os.IsNotExist(err) {
			respond.Error(w, http.StatusNotFound, errors.New("file not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if info.IsDir() {
		respond.Error(w, http.StatusBadRequest, errors.New("path is a directory, not a file"))
		return
	}

	content, err := os.ReadFile(targetFile)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("read file: %w", err))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]interface{}{
		"path":    requestedPath,
		"content": string(content),
		"size":    len(content),
	})
}

// WriteFileRequest is the request body for writing a file.
type WriteFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// WriteWorkspaceFile writes content to a file in a workspace.
func (h *Handler) WriteWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	workspaceID := chi.URLParam(r, "id")
	if workspaceID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	workspacePath, err := h.getWorkspacePath(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	var req WriteFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.Path == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	if workspacePath == "" {
		h.writeRuntimeWorkspaceFile(w, r, workspaceID, req)
		return
	}
	if err := validateWorkspacePath(workspacePath, req.Path); err != nil {
		respond.Error(w, http.StatusForbidden, err)
		return
	}
	if !h.authorizeWorkspaceOperation(w, r, workspaceID, workspacePath, capability.OpWriteFile, req.Path, nil) {
		return
	}

	targetFile := filepath.Join(workspacePath, req.Path)
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(targetFile), 0755); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("create directory: %w", err))
		return
	}

	if err := os.WriteFile(targetFile, []byte(req.Content), 0644); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("write file: %w", err))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{
		"status": "written",
		"path":   req.Path,
	})
}

// PatchRequest is the request body for applying a patch.
type PatchRequest struct {
	Patch string `json:"patch"`
}

// ApplyWorkspacePatch applies a git patch to the workspace.
func (h *Handler) ApplyWorkspacePatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	workspaceID := chi.URLParam(r, "id")
	if workspaceID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	workspacePath, err := h.getWorkspacePath(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	var req PatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.Patch == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("patch is required"))
		return
	}
	if workspacePath == "" {
		h.applyRuntimeWorkspacePatch(w, r, workspaceID, req)
		return
	}
	if !h.authorizeWorkspaceOperation(w, r, workspaceID, workspacePath, capability.OpApplyPatch, "workspace patch", nil) {
		return
	}

	// Write patch to temp file
	patchFile := filepath.Join(workspacePath, ".tmp_patch")
	if err := os.WriteFile(patchFile, []byte(req.Patch), 0600); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("write patch file: %w", err))
		return
	}
	defer os.Remove(patchFile)

	cmd := exec.CommandContext(ctx, "git", "-C", workspacePath, "apply", "--", patchFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("patch failed: %w (output: %s)", err, string(output)))
		return
	}

	respond.JSON(w, http.StatusOK, map[string]string{
		"status": "patched",
		"output": strings.TrimSpace(string(output)),
	})
}

// ExecRequest is the request body for executing a command.
type ExecRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds, default 60
}

// ExecWorkspaceCommand executes a shell command in the workspace directory.
func (h *Handler) ExecWorkspaceCommand(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	workspaceID := chi.URLParam(r, "id")
	if workspaceID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	workspacePath, err := h.getWorkspacePath(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if req.Command == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("command is required"))
		return
	}
	if workspacePath == "" {
		h.execRuntimeWorkspaceCommand(w, r, workspaceID, req)
		return
	}
	if !h.authorizeWorkspaceOperation(w, r, workspaceID, workspacePath, capability.OpRunCommand, req.Command, map[string]any{
		"command": req.Command,
	}) {
		return
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 60
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Use shell to run the command for proper argument parsing
	cmd := exec.CommandContext(execCtx, "sh", "-c", req.Command)
	cmd.Dir = workspacePath
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second

	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			exitCode = -1 // timeout
			respond.Error(w, http.StatusGatewayTimeout, fmt.Errorf("command timed out after %ds", timeout))
			return
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			respond.Error(w, http.StatusInternalServerError, fmt.Errorf("command failed: %w", err))
			return
		}
	}

	respond.JSON(w, http.StatusOK, map[string]interface{}{
		"command":   req.Command,
		"stdout":    string(output),
		"exit_code": exitCode,
	})
}

// StartServiceRequest is the request body for starting a dev service.
type StartServiceRequest struct {
	Command string `json:"command"`
	Port    int    `json:"port"`
	Name    string `json:"name,omitempty"`
}

// StartWorkspaceService starts a development service in the workspace.
func (h *Handler) StartWorkspaceService(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	workspaceID := chi.URLParam(r, "id")
	if workspaceID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	var req StartServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("command is required"))
		return
	}
	if req.Port < 0 || req.Port > 65535 {
		respond.Error(w, http.StatusBadRequest, errors.New("port must be between 0 and 65535"))
		return
	}
	serviceID, err := serviceIDFromRequest(req.Name)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	workspacePath, err := h.getWorkspacePath(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if workspacePath == "" {
		h.startRuntimeWorkspaceService(w, r, workspaceID, req, serviceID)
		return
	}
	if !h.authorizeWorkspaceOperation(w, r, workspaceID, workspacePath, capability.OpRunCommand, req.Command, map[string]any{
		"command": req.Command,
		"port":    req.Port,
		"service": serviceID,
	}) {
		return
	}
	if err := validateWorkspacePath(workspacePath, "."); err != nil {
		respond.Error(w, http.StatusForbidden, err)
		return
	}

	serviceDir, err := localWorkspaceServiceDir(workspaceID, serviceID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	if err := os.MkdirAll(serviceDir, 0755); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("create service directory: %w", err))
		return
	}
	logPath := filepath.Join(serviceDir, "service.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("open service log: %w", err))
		return
	}
	defer logFile.Close()

	cmd := exec.Command("sh", "-c", req.Command)
	cmd.Dir = workspacePath
	cmd.Env = os.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("start service: %w", err))
		return
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(filepath.Join(serviceDir, "pid"), []byte(fmt.Sprintf("%d\n", pid)), 0644); err != nil {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("write service pid: %w", err))
		return
	}
	if err := os.WriteFile(filepath.Join(serviceDir, "command"), []byte(req.Command+"\n"), 0644); err != nil {
		h.logger.Warn("failed to write service command metadata", "workspace_id", workspaceID, "service_id", serviceID, "error", err)
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			h.logger.Debug("workspace service exited", "workspace_id", workspaceID, "service_id", serviceID, "error", err)
		}
	}()

	respond.JSON(w, http.StatusAccepted, map[string]any{
		"service_id": serviceID,
		"status":     "running",
		"pid":        pid,
		"port":       req.Port,
		"log_path":   filepath.ToSlash(logPath),
	})
}

// StopServiceRequest is the request body for stopping a dev service.
type StopServiceRequest struct {
	ServiceID string `json:"service_id"`
}

// StopWorkspaceService stops a development service in the workspace.
func (h *Handler) StopWorkspaceService(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	workspaceID := chi.URLParam(r, "id")
	if workspaceID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace id is required"))
		return
	}

	if err := authz.AuthorizeWorkspace(ctx, h.db, user, workspaceID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
		return
	}

	var req StopServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	serviceID, err := validateServiceID(req.ServiceID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}

	workspacePath, err := h.getWorkspacePath(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if workspacePath == "" {
		h.stopRuntimeWorkspaceService(w, r, workspaceID, serviceID)
		return
	}
	if !h.authorizeWorkspaceOperation(w, r, workspaceID, workspacePath, capability.OpRunCommand, "stop service "+serviceID, map[string]any{
		"service": serviceID,
		"action":  "stop",
	}) {
		return
	}

	serviceDir, err := localWorkspaceServiceDir(workspaceID, serviceID)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, err)
		return
	}
	pidPath := filepath.Join(serviceDir, "pid")
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			respond.Error(w, http.StatusNotFound, errors.New("service not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("read service pid: %w", err))
		return
	}
	pid, err := parseServicePID(string(pidData))
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("stop service: %w", err))
		return
	}
	_ = os.Remove(pidPath)
	respond.JSON(w, http.StatusOK, map[string]string{
		"service_id": serviceID,
		"status":     "stopped",
	})
}

func (h *Handler) listRuntimeWorkspaceFiles(w http.ResponseWriter, r *http.Request, workspaceID string) {
	workspace, provider, err := h.getRuntimeWorkspace(r.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if provider == nil || workspace.RuntimeSessionID == nil {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace has no worktree path or runtime session"))
		return
	}

	requestedPath := r.URL.Query().Get("path")
	result, err := provider.ExecuteCommand(r.Context(), *workspace.RuntimeSessionID, runtimes.Command{
		Dir: requestedPath,
		Command: `for p in ./*; do
  [ -e "$p" ] || continue
  name=${p#./}
  case "$name" in .*) continue ;; esac
  if [ -d "$p" ]; then
    printf '%s\t%s\t%s\n' "$name" dir 0
  else
    size=$(wc -c < "$p" 2>/dev/null | tr -d ' ')
    printf '%s\t%s\t%s\n' "$name" file "${size:-0}"
  fi
done`,
		Timeout: 15 * time.Second,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("list runtime directory: %w", err))
		return
	}
	files := parseRuntimeFileList(requestedPath, result.Stdout)
	respond.JSON(w, http.StatusOK, files)
}

func (h *Handler) readRuntimeWorkspaceFile(w http.ResponseWriter, r *http.Request, workspaceID string) {
	workspace, provider, err := h.getRuntimeWorkspace(r.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if provider == nil || workspace.RuntimeSessionID == nil {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace has no worktree path or runtime session"))
		return
	}
	requestedPath := r.URL.Query().Get("path")
	if requestedPath == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("path query parameter is required"))
		return
	}
	content, err := provider.ReadFile(r.Context(), *workspace.RuntimeSessionID, requestedPath)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such file") {
			respond.Error(w, http.StatusNotFound, errors.New("file not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]interface{}{
		"path":    requestedPath,
		"content": string(content),
		"size":    len(content),
	})
}

func (h *Handler) writeRuntimeWorkspaceFile(w http.ResponseWriter, r *http.Request, workspaceID string, req WriteFileRequest) {
	workspace, provider, err := h.getRuntimeWorkspace(r.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if provider == nil || workspace.RuntimeSessionID == nil {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace has no worktree path or runtime session"))
		return
	}
	if !h.authorizeWorkspaceOperationForWorkspace(w, r, workspace, capability.OpWriteFile, req.Path, nil) {
		return
	}
	if err := provider.WriteFile(r.Context(), *workspace.RuntimeSessionID, req.Path, []byte(req.Content)); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("write runtime file: %w", err))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{
		"status": "written",
		"path":   req.Path,
	})
}

func (h *Handler) applyRuntimeWorkspacePatch(w http.ResponseWriter, r *http.Request, workspaceID string, req PatchRequest) {
	workspace, provider, err := h.getRuntimeWorkspace(r.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if provider == nil || workspace.RuntimeSessionID == nil {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace has no worktree path or runtime session"))
		return
	}
	if !h.authorizeWorkspaceOperationForWorkspace(w, r, workspace, capability.OpApplyPatch, "workspace patch", nil) {
		return
	}
	if err := provider.ApplyPatch(r.Context(), *workspace.RuntimeSessionID, req.Patch); err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("patch runtime workspace: %w", err))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{
		"status": "patched",
		"output": "",
	})
}

func (h *Handler) execRuntimeWorkspaceCommand(w http.ResponseWriter, r *http.Request, workspaceID string, req ExecRequest) {
	workspace, provider, err := h.getRuntimeWorkspace(r.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if provider == nil || workspace.RuntimeSessionID == nil {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace has no worktree path or runtime session"))
		return
	}
	if !h.authorizeWorkspaceOperationForWorkspace(w, r, workspace, capability.OpRunCommand, req.Command, map[string]any{
		"command": req.Command,
	}) {
		return
	}
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 60
	}
	result, err := provider.ExecuteCommand(r.Context(), *workspace.RuntimeSessionID, runtimes.Command{
		Command: req.Command,
		Timeout: time.Duration(timeout) * time.Second,
	})
	if err != nil {
		if errors.Is(err, runtimes.ErrCommandTimeout) {
			respond.Error(w, http.StatusGatewayTimeout, fmt.Errorf("command timed out after %ds", timeout))
			return
		}
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("runtime command failed: %w", err))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]interface{}{
		"command":   req.Command,
		"stdout":    result.Stdout + result.Stderr,
		"exit_code": result.ExitCode,
	})
}

func (h *Handler) startRuntimeWorkspaceService(w http.ResponseWriter, r *http.Request, workspaceID string, req StartServiceRequest, serviceID string) {
	workspace, provider, err := h.getRuntimeWorkspace(r.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if provider == nil || workspace.RuntimeSessionID == nil {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace has no worktree path or runtime session"))
		return
	}
	if !h.authorizeWorkspaceOperationForWorkspace(w, r, workspace, capability.OpRunCommand, req.Command, map[string]any{
		"command": req.Command,
		"port":    req.Port,
		"service": serviceID,
	}) {
		return
	}

	command := fmt.Sprintf(`set -eu
dir=".dev-plane/services/%s"
mkdir -p "$dir"
log="$dir/service.log"
cmd=%s
nohup sh -c "$cmd" > "$log" 2>&1 &
pid="$!"
printf '%%s\n' "$pid" > "$dir/pid"
printf '%%s\n' "$cmd" > "$dir/command"
printf '%%s\n' "$pid"`, serviceID, shellSingleQuote(req.Command))
	result, err := provider.ExecuteCommand(r.Context(), *workspace.RuntimeSessionID, runtimes.Command{
		Command: command,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("start runtime service: %w", err))
		return
	}
	if result.ExitCode != 0 {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("start runtime service exited %d: %s", result.ExitCode, result.Stderr))
		return
	}
	pid, _ := parseServicePID(result.Stdout)
	respond.JSON(w, http.StatusAccepted, map[string]any{
		"service_id": serviceID,
		"status":     "running",
		"pid":        pid,
		"port":       req.Port,
		"log_path":   filepath.ToSlash(filepath.Join(".dev-plane", "services", serviceID, "service.log")),
	})
}

func (h *Handler) stopRuntimeWorkspaceService(w http.ResponseWriter, r *http.Request, workspaceID, serviceID string) {
	workspace, provider, err := h.getRuntimeWorkspace(r.Context(), workspaceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("workspace not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}
	if provider == nil || workspace.RuntimeSessionID == nil {
		respond.Error(w, http.StatusBadRequest, errors.New("workspace has no worktree path or runtime session"))
		return
	}
	if !h.authorizeWorkspaceOperationForWorkspace(w, r, workspace, capability.OpRunCommand, "stop service "+serviceID, map[string]any{
		"service": serviceID,
		"action":  "stop",
	}) {
		return
	}

	command := fmt.Sprintf(`set -eu
dir=".dev-plane/services/%s"
pid_file="$dir/pid"
if [ ! -f "$pid_file" ]; then
  exit 44
fi
pid="$(cat "$pid_file")"
if kill -TERM "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null; then
  rm -f "$pid_file"
  printf 'stopped\n'
else
  rm -f "$pid_file"
  exit 44
fi`, serviceID)
	result, err := provider.ExecuteCommand(r.Context(), *workspace.RuntimeSessionID, runtimes.Command{
		Command: command,
		Timeout: 10 * time.Second,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("stop runtime service: %w", err))
		return
	}
	if result.ExitCode == 44 {
		respond.Error(w, http.StatusNotFound, errors.New("service not found"))
		return
	}
	if result.ExitCode != 0 {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("stop runtime service exited %d: %s", result.ExitCode, result.Stderr))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]string{
		"service_id": serviceID,
		"status":     "stopped",
	})
}

func serviceIDFromRequest(name string) (string, error) {
	if strings.TrimSpace(name) != "" {
		return validateServiceID(name)
	}
	return fmt.Sprintf("svc-%d", time.Now().UnixNano()), nil
}

func localWorkspaceServiceDir(workspaceID, serviceID string) (string, error) {
	workspaceComponent, err := validateStatePathComponent(workspaceID, "workspace id")
	if err != nil {
		return "", err
	}
	serviceComponent, err := validateStatePathComponent(serviceID, "service_id")
	if err != nil {
		return "", err
	}
	return filepath.Join(workspaceRuntimeBaseDir(), "local-services", workspaceComponent, serviceComponent), nil
}

func validateStatePathComponent(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(value) > 120 {
		return "", fmt.Errorf("%s is too long", field)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("%s may only contain letters, numbers, hyphens, and underscores", field)
	}
	return value, nil
}

func validateServiceID(serviceID string) (string, error) {
	return validateStatePathComponent(serviceID, "service_id")
}

func parseServicePID(raw string) (int, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, errors.New("service pid is empty")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid service pid %q", fields[0])
	}
	return pid, nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

type runtimeFileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

func parseRuntimeFileList(parentPath, output string) []runtimeFileEntry {
	var files []runtimeFileEntry
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		var size int64
		_, _ = fmt.Sscanf(parts[2], "%d", &size)
		files = append(files, runtimeFileEntry{
			Name:  parts[0],
			Path:  filepath.Join(parentPath, parts[0]),
			IsDir: parts[1] == "dir",
			Size:  size,
		})
	}
	if files == nil {
		return []runtimeFileEntry{}
	}
	return files
}

// getWorkspacePath retrieves the worktree_path for a workspace from the database.
func (h *Handler) getWorkspacePath(ctx context.Context, workspaceID string) (string, error) {
	var worktreePath sql.NullString
	err := h.db.QueryRowContext(ctx, `
		SELECT worktree_path FROM workspaces
		WHERE id = $1 AND deleted_at IS NULL
	`, workspaceID).Scan(&worktreePath)
	if err != nil {
		return "", err
	}
	if !worktreePath.Valid || worktreePath.String == "" {
		return "", nil
	}
	// Resolve symlinks
	resolved, err := filepath.EvalSymlinks(worktreePath.String)
	if err != nil {
		return worktreePath.String, nil // fallback to raw path
	}
	return resolved, nil
}

// validateWorkspacePath ensures the requested path stays within the workspace directory.
func validateWorkspacePath(workspacePath, requestedPath string) error {
	if filepath.IsAbs(requestedPath) {
		return fmt.Errorf("absolute paths are not allowed: %s", requestedPath)
	}
	fullPath := filepath.Join(workspacePath, requestedPath)
	resolved, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		// If the file doesn't exist yet (for writes), validate the parent directory
		if os.IsNotExist(err) {
			resolved = fullPath
		} else {
			return err
		}
	}
	cleanWorkspace := filepath.Clean(workspacePath) + string(os.PathSeparator)
	if !strings.HasPrefix(resolved+string(os.PathSeparator), cleanWorkspace) && resolved != filepath.Clean(workspacePath) {
		return fmt.Errorf("path traversal detected: %s", requestedPath)
	}
	return nil
}

func (h *Handler) authorizeWorkspaceOperation(w http.ResponseWriter, r *http.Request, workspaceID, workspacePath, operation, resource string, details map[string]any) bool {
	return h.authorizeWorkspaceOperationForWorkspace(w, r, &models.Workspace{
		ID:              workspaceID,
		WorktreePath:    &workspacePath,
		RuntimeProvider: "local",
		Status:          models.WorkspaceStatusReady,
	}, operation, resource, details)
}

func (h *Handler) authorizeWorkspaceOperationForWorkspace(w http.ResponseWriter, r *http.Request, workspace *models.Workspace, operation, resource string, details map[string]any) bool {
	userClaims := auth.UserFromContext(r.Context())
	var user *models.User
	actorType := "human"
	if userClaims != nil {
		user = &models.User{
			ID:             userClaims.UserID,
			OrganizationID: userClaims.OrgID,
			Email:          userClaims.Email,
			Role:           userClaims.Role,
		}
	}
	if user == nil {
		actorType = "anonymous"
	}
	if details == nil {
		details = map[string]any{}
	}
	details["workspace_id"] = workspace.ID

	result, err := h.kernel().Evaluate(r.Context(), capability.Request{
		ActorType:    actorType,
		User:         user,
		Workspace:    workspace,
		Operation:    operation,
		Resource:     resource,
		SandboxState: workspaceSandboxState(workspace),
		Details:      details,
	})
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, fmt.Errorf("authorize workspace operation: %w", err))
		return false
	}
	if result.Effect == policies.EffectDeny {
		respond.Error(w, http.StatusForbidden, errors.New(result.Reason))
		return false
	}
	if result.RequiredApproval {
		respond.Error(w, http.StatusLocked, errors.New(result.Reason))
		return false
	}
	return true
}

func workspaceSandboxState(workspace *models.Workspace) string {
	if workspace == nil {
		return "unknown"
	}
	switch workspace.RuntimeProvider {
	case "docker", "gvisor", "firecracker", "kubernetes":
		return "isolated"
	case "local":
		return "trusted_local"
	default:
		return workspace.RuntimeProvider
	}
}
