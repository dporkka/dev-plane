//go:build !linux

package runtimes

import (
	"fmt"
	"runtime"
)

func isTmpfs(path string) bool {
	_ = path
	return false
}

func mountTmpfs(path string) error {
	return fmt.Errorf("tmpfs mounts are not supported on %s", runtime.GOOS)
}

func unmountTmpfs(path string) error {
	return fmt.Errorf("tmpfs unmounts are not supported on %s", runtime.GOOS)
}

func tmpfsSizeOption() string {
	return ""
}
