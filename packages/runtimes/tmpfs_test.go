package runtimes

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultWorkspaceBaseDir_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WORKSPACE_BASE_DIR", dir)
	got := DefaultWorkspaceBaseDir()
	if got != dir {
		t.Errorf("DefaultWorkspaceBaseDir() = %q, want %q", got, dir)
	}
}

func TestDefaultWorkspaceBaseDir_Fallback(t *testing.T) {
	t.Setenv("WORKSPACE_BASE_DIR", "")
	got := DefaultWorkspaceBaseDir()
	want := filepath.Join(os.TempDir(), "ai-dev-control-plane-workspaces")
	if got != want {
		t.Errorf("DefaultWorkspaceBaseDir() = %q, want %q", got, want)
	}
}

func TestEnsureTmpfsBaseDir_CreatesAndIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspaces")

	path1, err := EnsureTmpfsBaseDir(dir)
	if err != nil {
		t.Fatalf("first EnsureTmpfsBaseDir failed: %v", err)
	}
	if path1 != dir {
		t.Errorf("first path = %q, want %q", path1, dir)
	}

	path2, err := EnsureTmpfsBaseDir(dir)
	if err != nil {
		t.Fatalf("second EnsureTmpfsBaseDir failed: %v", err)
	}
	if path2 != dir {
		t.Errorf("second path = %q, want %q", path2, dir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat base dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("base dir is not a directory")
	}
}

func TestEnsureTmpfsBaseDir_RespectsDisableEnv(t *testing.T) {
	// Use a directory under the package tree rather than t.TempDir(), because
	 // /tmp may already be tmpfs and would mask the disable behavior.
	dir, err := os.MkdirTemp(".", "tmpfs-disabled-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("WORKSPACE_TMPFS_DISABLE", "1")

	got, err := EnsureTmpfsBaseDir(dir)
	if err != nil {
		t.Fatalf("EnsureTmpfsBaseDir failed: %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Errorf("path = %q, want %q", got, filepath.Clean(dir))
	}

	if runtime.GOOS == "linux" && isTmpfs(got) {
		t.Errorf("isTmpfs(%q) = true, want false when WORKSPACE_TMPFS_DISABLE is set", dir)
	}
}

func TestEnsureTmpfsBaseDir_SkipsNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("data"), 0644); err != nil {
		t.Fatalf("create existing file: %v", err)
	}

	got, err := EnsureTmpfsBaseDir(dir)
	if err != nil {
		t.Fatalf("EnsureTmpfsBaseDir failed: %v", err)
	}
	if got != dir {
		t.Errorf("path = %q, want %q", got, dir)
	}

	// The existing file must still be readable; the directory should not have
	// been mounted over.
	content, err := os.ReadFile(filepath.Join(dir, "existing.txt"))
	if err != nil {
		t.Fatalf("existing file disappeared after EnsureTmpfsBaseDir: %v", err)
	}
	if string(content) != "data" {
		t.Errorf("existing file content = %q, want %q", string(content), "data")
	}
}

func TestUnmountTmpfsBaseDir_IgnoresUnknownPath(t *testing.T) {
	dir := t.TempDir()
	if err := UnmountTmpfsBaseDir(dir); err != nil {
		t.Errorf("UnmountTmpfsBaseDir on unknown path returned error: %v", err)
	}
}

func TestUnmountTmpfsBaseDir_IgnoresNonTmpfsPath(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureTmpfsBaseDir(dir); err != nil {
		t.Fatalf("EnsureTmpfsBaseDir failed: %v", err)
	}

	// The directory is empty, so on Linux we may have mounted tmpfs. If not
	// tmpfs, unmount must be a no-op rather than an error.
	if err := UnmountTmpfsBaseDir(dir); err != nil {
		t.Errorf("UnmountTmpfsBaseDir returned error: %v", err)
	}
}

func TestIsTmpfs_DevShm(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("tmpfs detection is Linux-specific")
	}
	devShm := "/dev/shm"
	info, err := os.Stat(devShm)
	if err != nil || !info.IsDir() {
		t.Skip("/dev/shm not available")
	}
	if !isTmpfs(devShm) {
		t.Errorf("isTmpfs(%q) = false, want true", devShm)
	}
}

func TestIsTmpfs_RegularDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("tmpfs detection is Linux-specific")
	}
	// t.TempDir() may live under /tmp which is often tmpfs. Use a directory in
	// the package tree, which is typically on the project's regular filesystem.
	dir, err := os.MkdirTemp(".", "tmpfs-regular-dir-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	if isTmpfs(dir) {
		t.Errorf("isTmpfs(%q) = true, want false", dir)
	}
}
