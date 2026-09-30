package readiness

import (
	"testing"
	"testing/fstest"
)

func TestInspectReportsReadyWhenCriticalAndAdvisorySignalsExist(t *testing.T) {
	repo := fstest.MapFS{
		"package.json":             {Data: []byte(`{"scripts":{"test":"vitest","lint":"eslint .","build":"vite build"}}`)},
		"pnpm-lock.yaml":           {Data: []byte("lockfileVersion: '9.0'")},
		"Dockerfile":               {Data: []byte("FROM golang:1.26")},
		".github/workflows/ci.yml": {Data: []byte("name: ci")},
		".github/CODEOWNERS":       {Data: []byte("* @platform")},
		"README.md":                {Data: []byte("# repo")},
		"CONTRIBUTING.md":          {Data: []byte("# contributing")},
		"AGENTS.md":                {Data: []byte("# instructions")},
	}

	report, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if report.Status != StatusReady {
		t.Fatalf("Inspect().Status = %q, want %q", report.Status, StatusReady)
	}
}

func TestInspectBlocksNodeRepositoryWithoutLockfile(t *testing.T) {
	repo := fstest.MapFS{
		"package.json": {Data: []byte(`{"scripts":{"test":"vitest","lint":"eslint ."}}`)},
		"README.md":    {Data: []byte("# repo")},
	}

	report, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	check := findCheck(t, report, "dependencies")
	if check.Status != StatusBlocked {
		t.Fatalf("dependencies status = %q, want %q", check.Status, StatusBlocked)
	}
	if report.Status != StatusBlocked {
		t.Fatalf("overall status = %q, want %q", report.Status, StatusBlocked)
	}
}

func TestInspectBlocksRepositoryWithoutDeterministicTestCommand(t *testing.T) {
	repo := fstest.MapFS{
		"go.mod":    {Data: []byte("module example.com/repo\n\ngo 1.23\n")},
		"go.sum":    {Data: []byte("example checksum")},
		"README.md": {Data: []byte("# repo")},
	}

	report, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	check := findCheck(t, report, "verification")
	if check.Status != StatusBlocked {
		t.Fatalf("verification status = %q, want %q", check.Status, StatusBlocked)
	}
}

func TestInspectUsesAttentionWhenCriticalChecksPassButAdvisorySignalsAreMissing(t *testing.T) {
	repo := fstest.MapFS{
		"go.mod":   {Data: []byte("module example.com/repo\n\ngo 1.23\n")},
		"go.sum":   {Data: []byte("example checksum")},
		"Makefile": {Data: []byte("test:\n\tgo test ./...\n\nlint:\n\tgo vet ./...\n")},
	}

	report, err := Inspect(repo)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if report.Status != StatusAttention {
		t.Fatalf("Inspect().Status = %q, want %q", report.Status, StatusAttention)
	}
}

func findCheck(t *testing.T, report Report, id string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("check %q not found", id)
	return Check{}
}
