package runtimes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWorkspaceRevisionConcurrentAgentAndBrowserWrites(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.ts")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	expected := FileContentRevision([]byte("old"))
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, content := range []string{"browser", "agent"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			_, err := WriteLocalFileRevision(context.Background(), root, "main.ts", []byte(value), &expected)
			outcomes <- err
		}(content)
	}
	wg.Wait()
	close(outcomes)
	success, conflicts := 0, 0
	for err := range outcomes {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrFileRevisionConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
}

func TestWorkspaceRevisionMissingFileAndSymlinkWrite(t *testing.T) {
	root := t.TempDir()
	missing := ""
	if _, err := WriteLocalFileRevision(context.Background(), root, "nested/new.go", []byte("hi"), &missing); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLocalFileRevision(context.Background(), root, "nested/new.go", []byte("oops"), &missing); !errors.Is(err, ErrFileRevisionConflict) {
		t.Fatalf("existing file should conflict: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLocalFileRevision(context.Background(), root, "external/oops.go", []byte("bad"), &missing); !errors.Is(err, ErrUnsafeWorkspacePath) {
		t.Fatalf("symlink should be rejected: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "missing"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLocalFileRevision(context.Background(), root, "dangling", []byte("bad"), &missing); !errors.Is(err, ErrUnsafeWorkspacePath) {
		t.Fatalf("dangling symlink should be rejected: %v", err)
	}
}

func TestWorkspaceRevisionRejectsMissingPrecondition(t *testing.T) {
 root := t.TempDir()
 if err := os.WriteFile(filepath.Join(root, "existing.ts"), []byte("agent"), 0644); err != nil {
  t.Fatal(err)
 }
 if _, err := WriteLocalFileRevision(context.Background(), root, "existing.ts", []byte("overwrite"), nil); !errors.Is(err, ErrFileRevisionRequired) {
  t.Fatalf("nil revision unexpectedly admitted: %v", err)
 }
 after, err := os.ReadFile(filepath.Join(root, "existing.ts"))
 if err != nil || string(after) != "agent" {
  t.Fatalf("existing bytes changed: %q err=%v", after, err)
 }
}
