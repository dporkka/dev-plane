package repointel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStubIndexerDoesNotFollowSymlinkFiles(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	secretPath := filepath.Join(outside, "secret.go")
	if err := os.WriteFile(secretPath, []byte("package secret\nfunc hostSecretToken() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secretPath, filepath.Join(repo, "leak.go")); err != nil {
		t.Fatal(err)
	}

	indexer := NewStubIndexer(repo)
	if err := indexer.Index(context.Background(), repo); err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	results, err := indexer.Search(context.Background(), "hostSecretToken", 10)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("Search() followed symlink outside workspace: %#v", results)
	}
	if indexer.EntryCount() != 0 {
		t.Fatalf("EntryCount() = %d, want 0 for symlink-only repo", indexer.EntryCount())
	}
}
