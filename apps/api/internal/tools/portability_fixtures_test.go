package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
)

func TestPortableRepositoryFixtures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		dir            string
		language       string
		packageManager string
		testCommand    string
		keyFile        string
	}{
		{
			name:           "go service",
			dir:            "go-service",
			language:       "Go",
			packageManager: "go",
			testCommand:    "go test ./...",
			keyFile:        "go.mod",
		},
		{
			name:           "typescript package",
			dir:            "typescript-package",
			language:       "TypeScript",
			packageManager: "npm",
			testCommand:    "npm test",
			keyFile:        "package.json",
		},
		{
			name:           "rust cli",
			dir:            "rust-cli",
			language:       "Rust",
			packageManager: "cargo",
			testCommand:    "cargo test",
			keyFile:        "Cargo.toml",
		},
		{
			name:           "python package",
			dir:            "python-package",
			language:       "Python",
			packageManager: "python",
			testCommand:    "python -m pytest",
			keyFile:        "pyproject.toml",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := filepath.Join("testdata", "portable-repos", tt.dir)
			result, err := NewWorkspaceTools(testLogger()).InspectRepo(
				context.Background(),
				repo,
				json.RawMessage(`{}`),
			)
			if err != nil {
				t.Fatalf("InspectRepo(%s): %v", tt.dir, err)
			}

			var got struct {
				Languages      map[string]int `json:"languages"`
				PackageManager string         `json:"package_manager"`
				TestCommand    string         `json:"test_command"`
				KeyFiles       []string       `json:"key_files"`
			}
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("decode inspection: %v", err)
			}

			if got.Languages[tt.language] == 0 {
				t.Fatalf("languages[%q] = 0, inspection=%s", tt.language, result)
			}
			if got.PackageManager != tt.packageManager {
				t.Fatalf("package_manager = %q, want %q", got.PackageManager, tt.packageManager)
			}
			if got.TestCommand != tt.testCommand {
				t.Fatalf("test_command = %q, want %q", got.TestCommand, tt.testCommand)
			}
			if !slices.Contains(got.KeyFiles, tt.keyFile) {
				t.Fatalf("key_files = %v, want %q", got.KeyFiles, tt.keyFile)
			}
		})
	}
}

func TestDetectPackageManagerUsesDeterministicSpecificMarkers(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{name: "pnpm beats package json", files: []string{"package.json", "pnpm-lock.yaml"}, want: "pnpm"},
		{name: "yarn beats package json", files: []string{"package.json", "yarn.lock"}, want: "yarn"},
		{name: "npm lock beats package json", files: []string{"package.json", "package-lock.json"}, want: "npm"},
		{name: "uv beats generic pyproject", files: []string{"pyproject.toml", "uv.lock"}, want: "uv"},
		{name: "poetry beats generic pyproject", files: []string{"pyproject.toml", "poetry.lock"}, want: "poetry"},
		{name: "generic pyproject is generic python", files: []string{"pyproject.toml"}, want: "python"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range tt.files {
				writeFixtureFile(t, dir, name, "")
			}
			if got := detectPackageManager(dir); got != tt.want {
				t.Fatalf("detectPackageManager() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectTestCommandRespectsRepositoryToolchain(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "pnpm",
			files: map[string]string{
				"package.json":   `{"scripts":{"test":"node --test"}}`,
				"pnpm-lock.yaml": "",
			},
			want: "pnpm test",
		},
		{
			name: "yarn",
			files: map[string]string{
				"package.json": `{"scripts":{"test":"node --test"}}`,
				"yarn.lock":    "",
			},
			want: "yarn test",
		},
		{
			name: "npm",
			files: map[string]string{
				"package.json":     `{"scripts":{"test":"node --test"}}`,
				"package-lock.json": "",
			},
			want: "npm test",
		},
		{
			name: "uv",
			files: map[string]string{
				"pyproject.toml": "[tool.pytest.ini_options]\n",
				"uv.lock":        "",
			},
			want: "uv run pytest",
		},
		{
			name: "poetry",
			files: map[string]string{
				"pyproject.toml": "[tool.pytest.ini_options]\n",
				"poetry.lock":    "",
			},
			want: "poetry run pytest",
		},
		{
			name: "generic python with pytest",
			files: map[string]string{
				"pyproject.toml": "[tool.pytest.ini_options]\n",
			},
			want: "python -m pytest",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFixtureFile(t, dir, name, content)
			}
			if got := detectTestCommand(dir); got != tt.want {
				t.Fatalf("detectTestCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectTestCommandDoesNotGuessWithoutTestSignal(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
	}{
		{
			name:  "package json without test script",
			files: map[string]string{"package.json": `{"scripts":{"build":"tsc"}}`},
		},
		{
			name:  "generic pyproject without runner",
			files: map[string]string{"pyproject.toml": "[project]\nname = \"example\"\n"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFixtureFile(t, dir, name, content)
			}
			if got := detectTestCommand(dir); got != "" {
				t.Fatalf("detectTestCommand() = %q, want empty command", got)
			}
		})
	}
}

func TestRunTestsRequiresDetectedOrExplicitCommand(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "package.json", `{"scripts":{"build":"tsc"}}`)

	_, err := NewWorkspaceTools(testLogger()).RunTests(
		context.Background(),
		dir,
		json.RawMessage(`{}`),
	)
	if err == nil {
		t.Fatal("RunTests() error = nil, want no-test-command error")
	}
	if got := err.Error(); got != "no test command detected; provide command explicitly" {
		t.Fatalf("RunTests() error = %q", got)
	}
}
