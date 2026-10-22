//go:build linux

package devplane

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

const MaxReadBytes = 65536

// Workspace exposes a directory descriptor-relative, symlink-denying read API.
// An authorized root is selected explicitly when starting the process.
// No arbitrary shell or write API exists in this release.
type Workspace struct {
	Root string
	fd   int
}

type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func NewWorkspace(root string) (*Workspace, error) {
	if root == "" {
		return nil, errors.New("workspace path must be specified")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return nil, err
	}
	fd, err := syscall.Open(abs, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	return &Workspace{Root: abs, fd: fd}, nil
}

func (w *Workspace) Close() error { return syscall.Close(w.fd) }

func pathParts(path string) ([]string, error) {
	if path == "" || path == "." {
		return nil, nil
	}
	if filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return nil, errors.New("absolute and invalid paths are denied")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == ".." || part == "." || part == "" || strings.Contains(part, "\\") {
			return nil, errors.New("invalid relative path component")
		}
		// Avoid revealing common local secrets, even in explicitly selected workspaces.
		lower := strings.ToLower(part)
		if lower == ".git" || strings.HasPrefix(lower, ".env") || lower == ".ssh" || lower == ".aws" || lower == ".gnupg" || lower == ".kube" || lower == ".secrets" || lower == ".gitconfig" || lower == ".netrc" || lower == ".npmrc" || strings.HasPrefix(lower, "id_rsa") || strings.HasPrefix(lower, "id_ed25519") || strings.HasPrefix(lower, "id_ecdsa") || strings.HasPrefix(lower, "credentials.") || strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".key") {
			return nil, errors.New("sensitive path denied")
		}
	}
	return parts, nil
}

// openRelative walks every directory with O_NOFOLLOW to close the TOCTOU
// symlink gap introduced by filepath.EvalSymlinks + os.Open.
func (w *Workspace) openRelative(path string, wantDirectory bool) (int, error) {
	parts, err := pathParts(path)
	if err != nil {
		return -1, err
	}
	current, err := syscall.Dup(w.fd)
	if err != nil {
		return -1, err
	}
	if len(parts) == 0 {
		if wantDirectory {
			return current, nil
		}
		syscall.Close(current)
		return -1, errors.New("expected a regular file")
	}
	for i, part := range parts {
		flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
		if i < len(parts)-1 || wantDirectory {
			flags |= syscall.O_DIRECTORY
		}
		next, openErr := syscall.Openat(current, part, flags, 0)
		syscall.Close(current)
		if openErr != nil {
			return -1, fmt.Errorf("path inaccessible: %w", openErr)
		}
		current = next
	}
	return current, nil
}

func (w *Workspace) ReadFile(path string, offset int64, maxBytes int) (string, error) {
	if offset < 0 || maxBytes < 0 || maxBytes > MaxReadBytes {
		return "", errors.New("invalid read offset or size")
	}
	if maxBytes == 0 {
		maxBytes = MaxReadBytes
	}
	fd, err := w.openRelative(path, false)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("only regular files can be read")
	}
	content := make([]byte, maxBytes)
	n, err := file.ReadAt(content, offset)
	if err != nil && err != io.EOF {
		return "", err
	}
	content = content[:n]
	if !utf8.Valid(content) {
		return "", errors.New("file is binary or slice splits UTF-8 encoding")
	}
	return string(content), nil
}

func (w *Workspace) ListDir(path string) ([]Entry, error) {
	fd, err := w.openRelative(path, true)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	children, err := f.ReadDir(2001)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(children) > 2000 {
		return nil, errors.New("directory too large; narrow path")
	}
	items := make([]Entry, 0, len(children))
	for _, child := range children {
		if _, err := pathParts(child.Name()); err != nil {
			continue
		}
		kind := "file"
		if child.IsDir() {
			kind = "directory"
		} else if child.Type()&os.ModeSymlink != 0 {
			kind = "symlink"
		} else if !child.Type().IsRegular() {
			kind = "other"
		}
		items = append(items, Entry{Name: child.Name(), Type: kind})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}
