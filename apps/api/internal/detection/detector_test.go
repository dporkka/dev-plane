package detection

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func mkdir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", name, err)
	}
}

func TestDetectPackageManager(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		manager string
	}{
		{
			name:    "npm",
			files:   map[string]string{"package-lock.json": ""},
			manager: "npm",
		},
		{
			name:    "yarn",
			files:   map[string]string{"yarn.lock": ""},
			manager: "yarn",
		},
		{
			name:    "pnpm",
			files:   map[string]string{"pnpm-lock.yaml": ""},
			manager: "pnpm",
		},
		{
			name:    "go",
			files:   map[string]string{"go.mod": "module example.com/foo"},
			manager: "go",
		},
		{
			name:    "poetry",
			files:   map[string]string{"pyproject.toml": ""},
			manager: "poetry",
		},
		{
			name:    "pip",
			files:   map[string]string{"requirements.txt": ""},
			manager: "pip",
		},
		{
			name:    "cargo",
			files:   map[string]string{"Cargo.toml": ""},
			manager: "cargo",
		},
		{
			name:    "gradle",
			files:   map[string]string{"build.gradle": ""},
			manager: "gradle",
		},
		{
			name:    "maven",
			files:   map[string]string{"pom.xml": ""},
			manager: "maven",
		},
		{
			name:    "bundler",
			files:   map[string]string{"Gemfile": ""},
			manager: "bundler",
		},
		{
			name:    "composer",
			files:   map[string]string{"composer.json": ""},
			manager: "composer",
		},
		{
			name:    "none",
			files:   map[string]string{"README.md": ""},
			manager: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range tt.files {
				writeFile(t, dir, name, content)
			}
			if got := detectPackageManager(dir); got != tt.manager {
				t.Errorf("package manager = %q, want %q", got, tt.manager)
			}
		})
	}
}

func TestDetectJSFramework(t *testing.T) {
	tests := []struct {
		name      string
		pkg       string
		framework string
	}{
		{"next", `{"dependencies":{"next":"14.0.0"}}`, "Next.js"},
		{"react", `{"dependencies":{"react":"18.0.0"}}`, "React"},
		{"vue", `{"devDependencies":{"vue":"3.0.0"}}`, "Vue"},
		{"express", `{"dependencies":{"express":"4.0.0"}}`, "Express"},
		{"none", `{"dependencies":{"lodash":"4.0.0"}}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "package.json", tt.pkg)
			if got := detectJSFramework(dir); got != tt.framework {
				t.Errorf("framework = %q, want %q", got, tt.framework)
			}
		})
	}
}

func TestDetectGoFramework(t *testing.T) {
	tests := []struct {
		name      string
		mod       string
		framework string
	}{
		{"gin", "require github.com/gin-gonic/gin v1.9.0", "Gin"},
		{"echo", "require github.com/labstack/echo/v4 v4.11.0", "Echo"},
		{"chi", "require github.com/go-chi/chi/v5 v5.0.0", "Chi"},
		{"none", "module example.com/foo", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "go.mod", tt.mod)
			if got := detectGoFramework(dir); got != tt.framework {
				t.Errorf("framework = %q, want %q", got, tt.framework)
			}
		})
	}
}

func TestDetectPythonFramework(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		content   string
		framework string
	}{
		{"django requirements", "requirements.txt", "django>=4.0", "Django"},
		{"flask pyproject", "pyproject.toml", "flask", "Flask"},
		{"fastapi requirements", "requirements.txt", "fastapi", "FastAPI"},
		{"none", "requirements.txt", "requests", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, tt.filename, tt.content)
			if got := detectPythonFramework(dir); got != tt.framework {
				t.Errorf("framework = %q, want %q", got, tt.framework)
			}
		})
	}
}

func TestDetectJSCommands(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{
		"scripts": {
			"test": "vitest",
			"lint": "eslint .",
			"typecheck": "tsc --noEmit",
			"dev": "vite",
			"build": "vite build"
		}
	}`)

	test, lint, typecheck, dev, build := detectJSCommands(dir)
	if test != "vitest" {
		t.Errorf("test = %q, want vitest", test)
	}
	if lint != "eslint ." {
		t.Errorf("lint = %q, want eslint .", lint)
	}
	if typecheck != "tsc --noEmit" {
		t.Errorf("typecheck = %q, want tsc --noEmit", typecheck)
	}
	if dev != "vite" {
		t.Errorf("dev = %q, want vite", dev)
	}
	if build != "vite build" {
		t.Errorf("build = %q, want vite build", build)
	}
}

func TestDetectGoCommands(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", "module example.com/foo")
		test, lint, typecheck, dev, build := detectGoCommands(dir)
		if test != "go test ./..." {
			t.Errorf("test = %q, want go test ./...", test)
		}
		if build != "go build" {
			t.Errorf("build = %q, want go build", build)
		}
		if typecheck != "" {
			t.Errorf("typecheck = %q, want empty", typecheck)
		}
		_ = lint
		_ = dev
	})

	t.Run("with golangci-lint", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", "module example.com/foo")
		writeFile(t, dir, ".golangci.yml", "run:")
		_, lint, _, _, _ := detectGoCommands(dir)
		if lint != "golangci-lint run" {
			t.Errorf("lint = %q, want golangci-lint run", lint)
		}
	})

	t.Run("with Makefile", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", "module example.com/foo")
		writeFile(t, dir, "Makefile", "test:\n\techo test\nlint:\n\techo lint\ntypecheck:\n\techo typecheck\ndev:\n\techo dev\nbuild:\n\techo build")
		test, lint, typecheck, dev, build := detectGoCommands(dir)
		if test != "make test" {
			t.Errorf("test = %q, want make test", test)
		}
		if lint != "make lint" {
			t.Errorf("lint = %q, want make lint", lint)
		}
		if typecheck != "make typecheck" {
			t.Errorf("typecheck = %q, want make typecheck", typecheck)
		}
		if dev != "make dev" {
			t.Errorf("dev = %q, want make dev", dev)
		}
		if build != "make build" {
			t.Errorf("build = %q, want make build", build)
		}
	})
}

func TestDetectDockerfile(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"Dockerfile", true},
		{"dockerfile", true},
		{"Dockerfile.prod", true},
		{"README.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, tt.filename, "FROM node")
			if got := detectDockerfile(dir); got != tt.want {
				t.Errorf("detectDockerfile = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectDevcontainer(t *testing.T) {
	dir := t.TempDir()
	if detectDevcontainer(dir) {
		t.Error("expected no devcontainer")
	}
	mkdir(t, dir, ".devcontainer")
	if !detectDevcontainer(dir) {
		t.Error("expected devcontainer detected")
	}
}

func TestDetector_Detect(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{
		"dependencies": {"next": "14.0.0"},
		"scripts": {
			"test": "jest",
			"lint": "eslint .",
			"dev": "next dev",
			"build": "next build"
		}
	}`)
	writeFile(t, dir, "pnpm-lock.yaml", "")
	writeFile(t, dir, "Dockerfile", "FROM node")
	mkdir(t, dir, ".devcontainer")

	detector := NewDetector()
	config, err := detector.Detect(context.Background(), dir)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}

	if config.PackageManager != "pnpm" {
		t.Errorf("package_manager = %q, want pnpm", config.PackageManager)
	}
	if config.Framework != "Next.js" {
		t.Errorf("framework = %q, want Next.js", config.Framework)
	}
	if config.TestCommand != "jest" {
		t.Errorf("test_command = %q, want jest", config.TestCommand)
	}
	if !config.HasDockerfile {
		t.Error("expected HasDockerfile true")
	}
	if !config.HasDevcontainer {
		t.Error("expected HasDevcontainer true")
	}
}

func TestDetector_DetectResult(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/foo")

	workspaceID := "ws-1"
	result, err := NewDetector().DetectResult(context.Background(), dir, "repo-1", &workspaceID)
	if err != nil {
		t.Fatalf("detect result: %v", err)
	}

	if result.RepositoryID != "repo-1" {
		t.Errorf("repository_id = %q, want repo-1", result.RepositoryID)
	}
	if result.WorkspaceID == nil || *result.WorkspaceID != "ws-1" {
		t.Errorf("workspace_id = %v, want ws-1", result.WorkspaceID)
	}
	if result.PackageManager != "go" {
		t.Errorf("package_manager = %q, want go", result.PackageManager)
	}
}
