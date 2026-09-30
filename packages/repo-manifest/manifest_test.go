package repomanifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValidManifest(t *testing.T) {
	manifest, err := Parse([]byte(`{
		"schema_version": 1,
		"name": "example",
		"commands": {
			"test": {"run": "make test", "timeout_seconds": 600},
			"lint": {"run": "make lint", "timeout_seconds": 120},
			"typecheck": {"run": "make typecheck"},
			"build": {"run": "make build"},
			"dev": {"run": "make dev"}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if manifest.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.Name != "example" {
		t.Fatalf("Name = %q, want example", manifest.Name)
	}
	if manifest.Commands.Test == nil || manifest.Commands.Test.Run != "make test" {
		t.Fatalf("test command = %#v", manifest.Commands.Test)
	}
	if manifest.Commands.Test.TimeoutSeconds != 600 {
		t.Fatalf("test timeout = %d, want 600", manifest.Commands.Test.TimeoutSeconds)
	}
	if manifest.Commands.Build == nil || manifest.Commands.Build.Run != "make build" {
		t.Fatalf("build command = %#v", manifest.Commands.Build)
	}
}

func TestLoadReadsCanonicalFilename(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"schema_version":1,"commands":{"test":{"run":"go test ./..."}}}`)
	if err := os.WriteFile(filepath.Join(dir, Filename), data, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if manifest.Commands.Test == nil || manifest.Commands.Test.Run != "go test ./..." {
		t.Fatalf("test command = %#v", manifest.Commands.Test)
	}
}

func TestLoadMissingManifestPreservesNotExist(t *testing.T) {
	_, err := Load(t.TempDir())
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load missing error = %v, want os.ErrNotExist", err)
	}
}

func TestParseRejectsUnsupportedSchemaVersion(t *testing.T) {
	_, err := Parse([]byte(`{"schema_version":2}`))
	if err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("error = %v, want unsupported schema_version", err)
	}
}

func TestParseRejectsBlankCommand(t *testing.T) {
	_, err := Parse([]byte(`{"schema_version":1,"commands":{"test":{"run":"   "}}}`))
	if err == nil || !strings.Contains(err.Error(), "commands.test.run") {
		t.Fatalf("error = %v, want commands.test.run validation error", err)
	}
}

func TestParseRejectsTimeoutAboveLimit(t *testing.T) {
	_, err := Parse([]byte(`{"schema_version":1,"commands":{"test":{"run":"go test ./...","timeout_seconds":3601}}}`))
	if err == nil || !strings.Contains(err.Error(), "timeout_seconds") {
		t.Fatalf("error = %v, want timeout_seconds validation error", err)
	}
}
