package runtimes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	ErrFileRevisionConflict = errors.New("workspace file changed since supplied revision")
	ErrInvalidFileRevision  = errors.New("expected revision must be empty or a lowercase SHA-256 hex digest")
	ErrUnsafeWorkspacePath  = errors.New("unsafe workspace file path")
)

func FileContentRevision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func ValidateFileRevision(expected *string) error {
	if expected == nil || *expected == "" {
		return nil
	}
	if len(*expected) != 64 {
		return ErrInvalidFileRevision
	}
	for _, ch := range *expected {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return ErrInvalidFileRevision
		}
	}
	return nil
}

func matchesFileRevision(previous []byte, exists bool, expected *string) bool {
	if expected == nil {
		return true
	}
	if !exists {
		return *expected == ""
	}
	return *expected == FileContentRevision(previous)
}

// WithWorkspaceFileLock serializes cooperating writers on a single host.
// The key must identify the same physical file regardless of its caller.
func WithWorkspaceFileLock(ctx context.Context, key string, fn func() error) error {
	lockDir := filepath.Join(os.TempDir(), "dev-plane-workspace-write-locks")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return err
	}
	filename := filepath.Join(lockDir, FileContentRevision([]byte(key))+".lock")
	handle, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer handle.Close()
	for {
		err = syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
	return fn()
}

func readFileIfPresent(target string) ([]byte, bool, error) {
	content, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

// rejectSymlinkComponents prevents writes through existing symlinks. It
// cannot stop an uncooperative process racing directory replacement.
func rejectSymlinkComponents(root, relative string) (string, error) {
	if relative == "" || relative == "." || filepath.IsAbs(relative) || strings.ContainsRune(relative, '\x00') {
		return "", ErrUnsafeWorkspacePath
	}
	clean := filepath.Clean(relative)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", ErrUnsafeWorkspacePath
	}
	current := root
	for _, part := range strings.Split(clean, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnsafeWorkspacePath
		}
	}
	return filepath.Join(root, clean), nil
}

// WriteLocalFileRevision serializes cooperating API and agent writes to a
// local worktree and rejects an obsolete expected file revision.
func WriteLocalFileRevision(ctx context.Context, workspaceRoot, relative string, data []byte, expected *string) (string, error) {
	if err := ValidateFileRevision(expected); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", err
	}
	var revision string
	lockKey := "local:" + root + ":" + filepath.Clean(relative)
	err = WithWorkspaceFileLock(ctx, lockKey, func() error {
		target, err := rejectSymlinkComponents(root, relative)
		if err != nil {
			return err
		}
		previous, exists, err := readFileIfPresent(target)
		if err != nil {
			return err
		}
		if !matchesFileRevision(previous, exists, expected) {
			return ErrFileRevisionConflict
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		temp, err := os.CreateTemp(filepath.Dir(target), ".dev-plane-edit-*")
		if err != nil {
			return err
		}
		defer os.Remove(temp.Name())
		defer temp.Close()
		mode := os.FileMode(0644)
		if exists {
			info, err := os.Stat(target)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return ErrUnsafeWorkspacePath
			}
			mode = info.Mode().Perm()
		}
		if err := temp.Chmod(mode); err != nil {
			return err
		}
		if _, err := temp.Write(data); err != nil {
			return err
		}
		if err := temp.Sync(); err != nil {
			return err
		}
		if err := temp.Close(); err != nil {
			return err
		}
		if err := os.Rename(temp.Name(), target); err != nil {
			return err
		}
		revision = FileContentRevision(data)
		return nil
	})
	return revision, err
}

// WriteRuntimeFileRevision coordinates API and agent-provider writes on one
// host. Remote nodes and arbitrary shell commands are NOT protected.
func WriteRuntimeFileRevision(ctx context.Context, provider Provider, sessionID, path string, data []byte, expected *string) (string, error) {
	if err := ValidateFileRevision(expected); err != nil {
		return "", err
	}
	if path == "" || path == "." || filepath.IsAbs(path) || strings.HasPrefix(filepath.Clean(path), "..") {
		return "", ErrUnsafeWorkspacePath
	}
	var revision string
	err := WithWorkspaceFileLock(ctx, "runtime:"+sessionID+":"+filepath.Clean(path), func() error {
		previous, err := provider.ReadFile(ctx, sessionID, path)
		exists := true
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "no such file") {
				exists = false
			} else {
				return fmt.Errorf("read runtime file before write: %w", err)
			}
		}
		if !matchesFileRevision(previous, exists, expected) {
			return ErrFileRevisionConflict
		}
		if err := provider.WriteFile(ctx, sessionID, path, data); err != nil {
			return err
		}
		revision = FileContentRevision(data)
		return nil
	})
	return revision, err
}
