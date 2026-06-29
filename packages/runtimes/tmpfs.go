package runtimes

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// baseDirState tracks whether a workspace base directory has been ensured and
// whether this process is the one that mounted tmpfs there.
type baseDirState struct {
	ensured     bool
	mountedByUs bool
}

var (
	baseDirMu    sync.Mutex
	ensuredPaths = map[string]*baseDirState{}
)

// DefaultWorkspaceBaseDir returns the default filesystem path for workspace data.
// It honors the WORKSPACE_BASE_DIR environment variable and falls back to a
// directory under the system temporary directory.
func DefaultWorkspaceBaseDir() string {
	if baseDir := os.Getenv("WORKSPACE_BASE_DIR"); baseDir != "" {
		return baseDir
	}
	return filepath.Join(os.TempDir(), "ai-dev-control-plane-workspaces")
}

// EnsureTmpfsBaseDir ensures baseDir exists and attempts to back it with tmpfs.
// The call is idempotent and safe to use from multiple goroutines and runtime
// providers. If tmpfs cannot be mounted (unsupported OS, insufficient privileges,
// the path is already tmpfs, or the directory is not empty), the directory is
// still returned so callers can fall back to normal disk storage.
func EnsureTmpfsBaseDir(baseDir string) (string, error) {
	if baseDir == "" {
		baseDir = DefaultWorkspaceBaseDir()
	}
	baseDir = filepath.Clean(baseDir)

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return "", fmt.Errorf("create workspace base directory %s: %w", baseDir, err)
	}

	baseDirMu.Lock()
	defer baseDirMu.Unlock()

	if state := ensuredPaths[baseDir]; state != nil && state.ensured {
		return baseDir, nil
	}

	if os.Getenv("WORKSPACE_TMPFS_DISABLE") == "1" || os.Getenv("WORKSPACE_TMPFS_DISABLE") == "true" {
		slog.Default().Debug("tmpfs backing disabled by WORKSPACE_TMPFS_DISABLE", "path", baseDir)
		ensuredPaths[baseDir] = &baseDirState{ensured: true}
		return baseDir, nil
	}

	if isTmpfs(baseDir) {
		ensuredPaths[baseDir] = &baseDirState{ensured: true}
		slog.Default().Debug("workspace base directory is already tmpfs-backed", "path", baseDir)
		return baseDir, nil
	}

	// Avoid mounting over a non-empty directory; doing so would hide any
	// existing workspace data on disk. An empty directory is the normal case
	// for a fresh base path.
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		ensuredPaths[baseDir] = &baseDirState{ensured: true}
		slog.Default().Warn("workspace base directory is not tmpfs-backed; cannot read directory contents", "path", baseDir, "error", err)
		return baseDir, nil
	}
	if len(entries) > 0 {
		ensuredPaths[baseDir] = &baseDirState{ensured: true}
		slog.Default().Warn("workspace base directory is not tmpfs-backed; directory is not empty", "path", baseDir)
		return baseDir, nil
	}

	if err := mountTmpfs(baseDir); err != nil {
		ensuredPaths[baseDir] = &baseDirState{ensured: true}
		slog.Default().Warn("workspace base directory is not tmpfs-backed; falling back to regular disk", "path", baseDir, "error", err)
		return baseDir, nil
	}

	ensuredPaths[baseDir] = &baseDirState{ensured: true, mountedByUs: true}
	slog.Default().Info("workspace base directory is tmpfs-backed", "path", baseDir)
	return baseDir, nil
}

// UnmountTmpfsBaseDir unmounts the tmpfs at baseDir only if this process mounted
// it. It is intended for graceful shutdown or test cleanup and ignores requests
// for paths that were not previously ensured or were already tmpfs before this
// process touched them.
func UnmountTmpfsBaseDir(baseDir string) error {
	if baseDir == "" {
		return nil
	}
	baseDir = filepath.Clean(baseDir)

	baseDirMu.Lock()
	defer baseDirMu.Unlock()

	state := ensuredPaths[baseDir]
	if state == nil || !state.mountedByUs {
		return nil
	}

	if !isTmpfs(baseDir) {
		delete(ensuredPaths, baseDir)
		return nil
	}

	if err := unmountTmpfs(baseDir); err != nil {
		return err
	}
	delete(ensuredPaths, baseDir)
	return nil
}
