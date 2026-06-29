//go:build linux

package runtimes

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func isTmpfs(path string) bool {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false
	}
	return stat.Type == unix.TMPFS_MAGIC
}

func mountTmpfs(path string) error {
	opts := tmpfsSizeOption()
	// nodev and nosuid reduce the attack surface of the shared RAM-backed
	// workspace directory. noexec is intentionally omitted because agents need
	// to run build tools and test executables inside the workspace.
	flags := unix.MS_NODEV | unix.MS_NOSUID
	if err := unix.Mount("tmpfs", path, "tmpfs", uintptr(flags), opts); err != nil {
		return fmt.Errorf("mount tmpfs at %s: %w", path, err)
	}
	return nil
}

func unmountTmpfs(path string) error {
	if err := unix.Unmount(path, 0); err != nil {
		return fmt.Errorf("unmount tmpfs at %s: %w", path, err)
	}
	return nil
}

func tmpfsSizeOption() string {
	if size := os.Getenv("WORKSPACE_TMPFS_SIZE"); size != "" {
		return "size=" + size
	}
	return ""
}
