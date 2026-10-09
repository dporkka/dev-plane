package handlers

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

    "github.com/ai-dev-control-plane/runtimes"
)

var (
    errWorkspaceRevisionConflict = errors.New("workspace file changed since the supplied revision")
    errInvalidWorkspaceRevision = errors.New("expected_revision must be an empty string (missing file) or a 64-character SHA-256 hex digest")
)

// workspaceContentRevision is the SHA-256 digest of exact file bytes, not the Git HEAD.
func workspaceContentRevision(data []byte) string {
    digest := sha256.Sum256(data)
    return hex.EncodeToString(digest[:])
}

func validateWorkspaceRevision(expected *string) error {
    if expected == nil || *expected == "" { return nil }
    if len(*expected) != 64 { return errInvalidWorkspaceRevision }
    for _, ch := range *expected {
        if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
            return errInvalidWorkspaceRevision
        }
    }
    return nil
}

func revisionMatches(content []byte, exists bool, expected *string) bool {
    if expected == nil { return true } // compatibility for trusted legacy clients
    if !exists { return *expected == "" }
    return *expected == workspaceContentRevision(content)
}

// Locks serialize cooperating Dev Plane API writers on the same host. Agents
// writing via arbitrary shell commands do NOT participate in this protocol.
func withWorkspaceFileWriteLock(ctx context.Context, key string, fn func() error) error {
    lockDir := filepath.Join(os.TempDir(), "dev-plane-workspace-write-locks")
    if err := os.MkdirAll(lockDir, 0700); err != nil { return err }
    digest := workspaceContentRevision([]byte(key))
    handle, err := os.OpenFile(filepath.Join(lockDir, digest+".lock"), os.O_CREATE|os.O_RDWR, 0600)
    if err != nil { return err }
    defer handle.Close()
    for {
        err = syscall.Flock(int(handle.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
        if err == nil { break }
        if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN { return err }
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-time.After(10 * time.Millisecond):
        }
    }
    defer syscall.Flock(int(handle.Fd()), syscall.LOCK_UN)
    return fn()
}

func readCurrentRevision(path string) ([]byte, bool, error) {
    content, err := os.ReadFile(path)
    if errors.Is(err, os.ErrNotExist) { return nil, false, nil }
    if err != nil { return nil, false, err }
    return content, true, nil
}

func writeLocalWorkspaceRevision(ctx context.Context, workspacePath, relativePath string, data []byte, expected *string) (string, error) {
    if err := validateWorkspaceRevision(expected); err != nil { return "", err }
    var revision string
    err := withWorkspaceFileWriteLock(ctx, "local:"+workspacePath+":"+relativePath, func() error {
        if err := validateWorkspacePath(workspacePath, relativePath); err != nil { return err }
        target := filepath.Join(workspacePath, relativePath)
        previous, exists, err := readCurrentRevision(target)
        if err != nil { return err }
        if !revisionMatches(previous, exists, expected) { return errWorkspaceRevisionConflict }
        if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil { return err }
        // Rename avoids exposing partially-written bytes to readers.
        temp, err := os.CreateTemp(filepath.Dir(target), ".dev-plane-edit-*")
        if err != nil { return err }
        defer os.Remove(temp.Name())
        defer temp.Close()
        mode := os.FileMode(0644)
        if exists {
            if info, err := os.Stat(target); err == nil { mode = info.Mode().Perm() }
        }
        if err := temp.Chmod(mode); err != nil { return err }
        if _, err := temp.Write(data); err != nil { return err }
        if err := temp.Sync(); err != nil { return err }
        if err := temp.Close(); err != nil { return err }
        if err := os.Rename(temp.Name(), target); err != nil { return err }
        revision = workspaceContentRevision(data)
        return nil
    })
    return revision, err
}

// Runtime API writes use the same revision contract. The lock only coordinates
// API processes sharing this host's lock directory, not uncooperative shell
// writers or runtime nodes with independent filesystems.
func writeRuntimeWorkspaceRevision(ctx context.Context, workspaceID string, provider runtimes.Provider, sessionID, path string, data []byte, expected *string) (string, error) {
    if err := validateWorkspaceRevision(expected); err != nil { return "", err }
    var revision string
    err := withWorkspaceFileWriteLock(ctx, "runtime:"+workspaceID+":"+path, func() error {
        previous, err := provider.ReadFile(ctx, sessionID, path)
        exists := true
        if err != nil {
            if errors.Is(err, os.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "no such file") {
                exists = false
            } else {
                return fmt.Errorf("read before runtime write: %w", err)
            }
        }
        if !revisionMatches(previous, exists, expected) { return errWorkspaceRevisionConflict }
        if err := provider.WriteFile(ctx, sessionID, path, data); err != nil { return err }
        revision = workspaceContentRevision(data)
        return nil
    })
    return revision, err
}
