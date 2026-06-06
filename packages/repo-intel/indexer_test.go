package repointel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestStubIndexerIndexesSymbolsAndSearchesContent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "main.go", `package main

type Server struct{}

func StartServer() {}
`)
	writeTestFile(t, dir, "src/app.ts", `export interface Config {
  port: number
}

export const startApp = () => "listen"
`)

	indexer := NewStubIndexer(dir)
	if err := indexer.Index(context.Background(), dir); err != nil {
		t.Fatalf("index repo: %v", err)
	}

	symbols, err := indexer.GetSymbols(context.Background(), "main.go")
	if err != nil {
		t.Fatalf("get symbols: %v", err)
	}
	if len(symbols) != 2 {
		t.Fatalf("symbol count = %d, want 2: %#v", len(symbols), symbols)
	}
	assertEqual(t, symbols[0].Name, "Server")
	assertEqual(t, symbols[0].Kind, "struct")
	assertEqual(t, symbols[1].Name, "StartServer")
	assertEqual(t, symbols[1].Kind, "function")

	results, err := indexer.Search(context.Background(), "startserver", 10)
	if err != nil {
		t.Fatalf("search symbol: %v", err)
	}
	if len(results) == 0 || results[0].Symbol.Name != "StartServer" {
		t.Fatalf("expected StartServer search result, got %#v", results)
	}

	results, err = indexer.Search(context.Background(), "listen", 10)
	if err != nil {
		t.Fatalf("search content: %v", err)
	}
	if len(results) == 0 || results[0].Snippet == "" {
		t.Fatalf("expected content search result, got %#v", results)
	}
}

func TestStubIndexerGetDependencies(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", `module example.com/app

require github.com/example/direct v1.2.3

require (
	github.com/example/block v0.1.0
	github.com/example/indirect v0.2.0 // indirect
)
`)
	writeTestFile(t, dir, "package.json", `{
  "dependencies": {
    "react": "^19.0.0"
  },
  "devDependencies": {
    "typescript": "^5.0.0"
  }
}`)

	indexer := NewStubIndexer(dir)
	deps, err := indexer.GetDependencies(context.Background(), dir)
	if err != nil {
		t.Fatalf("get dependencies: %v", err)
	}

	want := map[string]string{
		"github.com/example/direct":   "go_modules",
		"github.com/example/block":    "go_modules",
		"github.com/example/indirect": "go_modules",
		"react":                       "npm",
		"typescript":                  "npm",
	}
	for name, source := range want {
		if !dependencyFound(deps, name, source) {
			t.Fatalf("missing dependency %s from %s in %#v", name, source, deps)
		}
	}
}

func TestStubIndexerGetDependenciesEmpty(t *testing.T) {
	dir := t.TempDir()
	indexer := NewStubIndexer(dir)

	deps, err := indexer.GetDependencies(context.Background(), dir)
	if err != nil {
		t.Fatalf("get dependencies: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("deps = %#v, want empty", deps)
	}
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create test dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func dependencyFound(deps []Dependency, name, source string) bool {
	for _, dep := range deps {
		if dep.Name == name && dep.Source == source {
			return true
		}
	}
	return false
}
