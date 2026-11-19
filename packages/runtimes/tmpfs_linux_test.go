//go:build linux

package runtimes

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// Check the filesystem independently; using isTmpfs to choose whether to
// test isTmpfs would conceal false positives on ordinary filesystems.
func TestIsTmpfs_RegularDir(t *testing.T) {
	var stat unix.Statfs_t
	if err := unix.Statfs(".", &stat); err != nil {
		t.Fatalf("stat checkout filesystem: %v", err)
	}
	if stat.Type == unix.TMPFS_MAGIC {
		t.Skip("checkout is tmpfs: no regular-filesystem fixture available")
	}

	dir, err := os.MkdirTemp(".", "tmpfs-regular-dir-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	if isTmpfs(dir) {
		t.Errorf("isTmpfs(%q) = true on a regular filesystem", dir)
	}
}
