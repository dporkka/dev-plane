package devplane

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAndListFilesScopedToWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "file.txt"), []byte("abcdefghij"), 0600); err != nil {
		t.Fatal(err)
	}
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	data, err := ws.ReadFile("src/file.txt", 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if data != "cdef" {
		t.Fatalf("got %q", data)
	}
	entries, err := ws.ListDir("src")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "file.txt" || entries[0].Type != "file" {
		t.Fatalf("entries: %#v", entries)
	}
}

func TestPathTraversalAndSymlinkEscapeDenied(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("must not read"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "secret-link")); err != nil {
		t.Fatal(err)
	}
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	for _, name := range []string{"../secret", "/etc/passwd", "escape/secret", "secret-link", "foo/../../etc/passwd"} {
		if _, err := ws.ReadFile(name, 0, 100); err == nil {
			t.Errorf("read should reject %q", name)
		}
	}
	if _, err := ws.ListDir("escape"); err == nil {
		t.Error("list should reject symlink")
	}
}

func TestFilesLimitAndInvalidOffsets(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), []byte(strings.Repeat("x", 100)), 0600)
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	for _, tc := range []struct {
		offset int64
		max    int
	}{{-1, 5}, {0, -1}, {0, 1000000}} {
		if _, err := ws.ReadFile("a", tc.offset, tc.max); err == nil {
			t.Errorf("should reject %+v", tc)
		}
	}
}

func TestSecretsExcludedAndSymlinksNeverFollowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("API_SECRET=x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "public.txt"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("public.txt", filepath.Join(dir, "shortcut")); err != nil {
		t.Fatal(err)
	}
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	if _, err := ws.ReadFile(".env", 0, 100); err == nil {
		t.Fatal("environment file was readable")
	}
	if _, err := ws.ReadFile("shortcut", 0, 100); err == nil {
		t.Fatal("symlink was readable")
	}
	entries, err := ws.ListDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == ".env" {
			t.Fatal("sensitive file disclosed in listing")
		}
	}
	found := false
	for _, e := range entries {
		if e.Name == "shortcut" && e.Type == "symlink" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected symlink metadata, got %#v", entries)
	}
}

func TestAdditionalSensitivePathNamesDenied(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".env.backup", ".gitconfig", "id_ecdsa", ".secrets", ".kube/config", "credentials.toml"} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("super secret"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	for _, name := range []string{".env.backup", ".gitconfig", "id_ecdsa", ".secrets", ".kube/config", "credentials.toml"} {
		if text, err := ws.ReadFile(name, 0, 100); err == nil {
			t.Errorf("sensitive %s was readable: %s", name, text)
		}
	}
}
