package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ai-dev-control-plane/readiness"
)

func TestRunInspectJSON(t *testing.T) {
	repo := t.TempDir()
	mustWriteInspectFile(t, repo, "go.mod", "module example.com/repo\n\ngo 1.23\n")
	mustWriteInspectFile(t, repo, "go.sum", "example checksum\n")
	mustWriteInspectFile(t, repo, "Makefile", "test:\n\tgo test ./...\n\nlint:\n\tgo vet ./...\n")

	var out bytes.Buffer
	if err := runInspectTo([]string{"--path", repo, "--json"}, &out); err != nil {
		t.Fatalf("runInspectTo() error = %v", err)
	}

	var report readiness.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if report.Status != readiness.StatusAttention {
		t.Fatalf("report status = %q, want %q", report.Status, readiness.StatusAttention)
	}
}

func TestRunInspectRejectsUnexpectedArguments(t *testing.T) {
	var out bytes.Buffer
	if err := runInspectTo([]string{"extra"}, &out); err == nil {
		t.Fatal("runInspectTo() error = nil, want usage error")
	}
}

func mustWriteInspectFile(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
