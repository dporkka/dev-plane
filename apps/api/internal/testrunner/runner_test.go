package testrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"log/slog"
)

func TestNewRunner(t *testing.T) {
	r := NewRunner(slog.Default())
	if r == nil {
		t.Fatal("NewRunner returned nil")
	}
	if r.logger == nil {
		t.Error("logger should not be nil")
	}
}

func TestRunner_findTestCommand(t *testing.T) {
	r := NewRunner(slog.Default())

	tests := []struct {
		name      string
		files     map[string]string
		config    *ProjectConfig
		wantCmd   string
		wantTimeout int
	}{
		{
			name:        "go project",
			files:       map[string]string{"go.mod": "module test\ngo 1.23\n"},
			wantCmd:     "go test ./...",
			wantTimeout: 300,
		},
		{
			name:        "node project",
			files:       map[string]string{"package.json": "{}"},
			wantCmd:     "npm test",
			wantTimeout: 300,
		},
		{
			name:        "rust project",
			files:       map[string]string{"Cargo.toml": ""},
			wantCmd:     "cargo test",
			wantTimeout: 300,
		},
		{
			name:        "python project",
			files:       map[string]string{"requirements.txt": ""},
			wantCmd:     "python -m pytest",
			wantTimeout: 300,
		},
		{
			name: "config override",
			files: map[string]string{},
			config: &ProjectConfig{
				TestCommand: "make test",
				TimeoutSec:  60,
			},
			wantCmd:     "make test",
			wantTimeout: 60,
		},
		{
			name:        "no project files",
			files:       map[string]string{},
			wantCmd:     "",
			wantTimeout: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			for fname, content := range tt.files {
				os.WriteFile(filepath.Join(tmpDir, fname), []byte(content), 0644)
			}
			gotCmd, gotTimeout := r.findTestCommand(tmpDir, tt.config)
			if gotCmd != tt.wantCmd {
				t.Errorf("findTestCommand() cmd = %q, want %q", gotCmd, tt.wantCmd)
			}
			if gotTimeout != tt.wantTimeout {
				t.Errorf("findTestCommand() timeout = %d, want %d", gotTimeout, tt.wantTimeout)
			}
		})
	}
}

func TestRunner_findLintCommand(t *testing.T) {
	r := NewRunner(slog.Default())

	tests := []struct {
		name    string
		files   map[string]string
		wantCmd string
	}{
		{
			name:    "go project",
			files:   map[string]string{"go.mod": "module test\ngo 1.23\n"},
			wantCmd: "go vet ./...",
		},
		{
			name:    "node with eslint",
			files:   map[string]string{"package.json": "{}", ".eslintrc.js": ""},
			wantCmd: "npx eslint .",
		},
		{
			name:    "rust project",
			files:   map[string]string{"Cargo.toml": ""},
			wantCmd: "cargo clippy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			for fname, content := range tt.files {
				os.WriteFile(filepath.Join(tmpDir, fname), []byte(content), 0644)
			}
			gotCmd, _ := r.findLintCommand(tmpDir, nil)
			if gotCmd != tt.wantCmd {
				t.Errorf("findLintCommand() = %q, want %q", gotCmd, tt.wantCmd)
			}
		})
	}
}

func TestRunner_findTypecheckCommand(t *testing.T) {
	r := NewRunner(slog.Default())

	tests := []struct {
		name    string
		files   map[string]string
		wantCmd string
	}{
		{
			name:    "go project",
			files:   map[string]string{"go.mod": "module test\ngo 1.23\n"},
			wantCmd: "go build ./...",
		},
		{
			name:    "typescript project",
			files:   map[string]string{"tsconfig.json": "{}"},
			wantCmd: "npx tsc --noEmit",
		},
		{
			name:    "rust project",
			files:   map[string]string{"Cargo.toml": ""},
			wantCmd: "cargo check",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			for fname, content := range tt.files {
				os.WriteFile(filepath.Join(tmpDir, fname), []byte(content), 0644)
			}
			gotCmd, _ := r.findTypecheckCommand(tmpDir, nil)
			if gotCmd != tt.wantCmd {
				t.Errorf("findTypecheckCommand() = %q, want %q", gotCmd, tt.wantCmd)
			}
		})
	}
}

func TestTestResult_Passed(t *testing.T) {
	result := &TestResult{
		Command:    "go test ./...",
		ExitCode:   0,
		Stdout:     "ok\n",
		Stderr:     "",
		Passed:     true,
		DurationMs: 1000,
	}
	if !result.Passed {
		t.Error("expected Passed to be true")
	}

	result2 := &TestResult{
		Command:    "go test ./...",
		ExitCode:   1,
		Passed:     false,
		DurationMs: 1000,
	}
	if result2.Passed {
		t.Error("expected Passed to be false")
	}
}

func TestAllResults_Overall(t *testing.T) {
	r := &AllResults{
		Lint: &TestResult{Passed: true},
		Tests: &TestResult{Passed: true},
		Overall: true,
	}
	if !r.Overall {
		t.Error("expected Overall to be true")
	}

	r2 := &AllResults{
		Lint: &TestResult{Passed: true},
		Tests: &TestResult{Passed: false},
		Overall: false,
	}
	if r2.Overall {
		t.Error("expected Overall to be false")
	}
}

func TestAllResults_ResultSummary(t *testing.T) {
	r := &AllResults{
		Lint: &TestResult{
			Command:    "go vet ./...",
			Passed:     true,
			DurationMs: 5000,
		},
		Typecheck: &TestResult{
			Command:    "go build ./...",
			Passed:     true,
			DurationMs: 10000,
		},
		Tests: &TestResult{
			Command:    "go test ./...",
			Passed:     true,
			DurationMs: 30000,
		},
		Overall: true,
	}

	summary := r.ResultSummary()
	if summary == "" {
		t.Error("ResultSummary should not be empty")
	}
	if summary == "Overall: PASS" || summary == "Overall: FAIL" {
		t.Error("ResultSummary should include details, not just overall")
	}
}

func TestAllResults_ToJSON(t *testing.T) {
	r := &AllResults{
		Lint: &TestResult{
			Command:  "go vet ./...",
			Passed:   true,
			ExitCode: 0,
		},
		Overall: true,
	}

	data, err := r.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}

	var decoded AllResults
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Lint == nil {
		t.Fatal("Lint should not be nil after round-trip")
	}
	if decoded.Lint.Command != "go vet ./..." {
		t.Errorf("Command = %q, want %q", decoded.Lint.Command, "go vet ./...")
	}
	if !decoded.Lint.Passed {
		t.Error("Passed should be true")
	}
	if !decoded.Overall {
		t.Error("Overall should be true")
	}
}

func TestParseTestOutput(t *testing.T) {
	output := `ok  	github.com/example/pkg1	0.5s
ok  	github.com/example/pkg2	1.2s
?  	github.com/example/pkg3	[no test files]
FAIL	github.com/example/pkg4	0.3s
`
	passed, total, failed, skipped := ParseTestOutput(output, 1)
	if passed {
		t.Error("expected passed = false due to exit code 1")
	}
	if total == 0 {
		t.Error("expected non-zero total")
	}
	if failed == 0 {
		t.Error("expected non-zero failed count")
	}
	if skipped == 0 {
		t.Error("expected non-zero skipped count")
	}
}

func TestFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "exists.txt"), []byte(""), 0644)

	if !fileExists(tmpDir, "exists.txt") {
		t.Error("expected exists.txt to exist")
	}
	if fileExists(tmpDir, "notfound.txt") {
		t.Error("expected notfound.txt to not exist")
	}
}

func TestTruncateOutput(t *testing.T) {
	s := "short"
	if got := truncateOutput(s, 100); got != s {
		t.Errorf("truncateOutput(%q, 100) = %q, want %q", s, got, s)
	}

	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'a'
	}
	result := truncateOutput(string(long), 100)
	if len(result) <= 100 {
		t.Error("expected truncated result to have truncation marker")
	}
}

func TestRunner_RunAll_NoProject(t *testing.T) {
	// Test with empty directory - should return empty results
	tmpDir := t.TempDir()
	r := NewRunner(slog.Default())

	results, err := r.RunAll(context.Background(), tmpDir, nil)
	if err != nil {
		t.Fatalf("RunAll: %v", err)
	}
	if results == nil {
		t.Fatal("results should not be nil")
	}
	// With no project files, no checks run, so overall should pass
	if !results.Overall {
		t.Error("expected Overall to be true when no checks run")
	}
}

func TestProjectConfig_JSONRoundTrip(t *testing.T) {
	config := &ProjectConfig{
		Language:     "go",
		TestCommand:  "go test ./...",
		LintCommand:  "go vet ./...",
		TypecheckCmd: "go build ./...",
		Env: map[string]string{
			"CGO_ENABLED": "0",
		},
		TimeoutSec: 120,
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ProjectConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Language != config.Language {
		t.Errorf("Language = %q, want %q", decoded.Language, config.Language)
	}
	if decoded.TestCommand != config.TestCommand {
		t.Errorf("TestCommand = %q, want %q", decoded.TestCommand, config.TestCommand)
	}
	if decoded.TimeoutSec != config.TimeoutSec {
		t.Errorf("TimeoutSec = %d, want %d", decoded.TimeoutSec, config.TimeoutSec)
	}
	if decoded.Env["CGO_ENABLED"] != "0" {
		t.Errorf("Env[CGO_ENABLED] = %q, want %q", decoded.Env["CGO_ENABLED"], "0")
	}
}
