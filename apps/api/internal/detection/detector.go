// Package detection provides project configuration detection from repository files.
// It identifies package managers, frameworks, build/test/dev commands, and
// infrastructure configuration (Dockerfile, devcontainer) by scanning workspace
// contents.
package detection

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ai-dev-control-plane/models"
)

// Detector identifies project configuration from repository files.
type Detector struct{}

// NewDetector creates a new project configuration detector.
func NewDetector() *Detector {
	return &Detector{}
}

// Detect scans a workspace path and identifies the project configuration:
//   - Package manager (npm, yarn, pnpm, go, pip, poetry, cargo, gradle, maven)
//   - Framework (Next.js, React, Vue, Gin, Echo, Django, Rails, etc.)
//   - Commands: test, lint, typecheck, dev, build
//   - Presence of Dockerfile, .devcontainer
func (d *Detector) Detect(_ context.Context, workspacePath string) (*models.ProjectConfig, error) {
	config := &models.ProjectConfig{}

	// Step 1: Detect package manager
	config.PackageManager = detectPackageManager(workspacePath)

	// Step 2: Detect framework
	config.Framework = detectFramework(workspacePath, config.PackageManager)

	// Step 3: Detect commands
	config.TestCommand, config.LintCommand, config.TypecheckCommand, config.DevCommand, config.BuildCommand = detectCommands(workspacePath, config.PackageManager)

	// Step 4: Detect Dockerfile
	config.HasDockerfile = detectDockerfile(workspacePath)

	// Step 5: Detect devcontainer
	config.HasDevcontainer = detectDevcontainer(workspacePath)

	return config, nil
}

// DetectResult performs full detection and returns a DetectionResult suitable
// for persisting to the database.
func (d *Detector) DetectResult(ctx context.Context, workspacePath, repoID string, workspaceID *string) (*models.DetectionResult, error) {
	config, err := d.Detect(ctx, workspacePath)
	if err != nil {
		return nil, err
	}

	result := &models.DetectionResult{
		RepositoryID:     repoID,
		WorkspaceID:      workspaceID,
		PackageManager:   config.PackageManager,
		Framework:        config.Framework,
		TestCommand:      config.TestCommand,
		LintCommand:      config.LintCommand,
		TypecheckCommand: config.TypecheckCommand,
		DevCommand:       config.DevCommand,
		BuildCommand:     config.BuildCommand,
		HasDockerfile:    config.HasDockerfile,
		HasDevcontainer:  config.HasDevcontainer,
	}

	return result, nil
}

// detectPackageManager identifies the package manager by looking for
// lockfiles and configuration files.
func detectPackageManager(workspacePath string) string {
	checks := []struct {
		files   []string
		manager string
	}{
		{[]string{"pnpm-lock.yaml", ".pnpmfile.cjs"}, "pnpm"},
		{[]string{"yarn.lock", ".yarnrc.yml", ".yarnrc"}, "yarn"},
		{[]string{"package-lock.json", "npm-shrinkwrap.json"}, "npm"},
		{[]string{"bun.lockb", "bun.lock"}, "bun"},
		{[]string{"go.mod"}, "go"},
		{[]string{"poetry.lock", "pyproject.toml"}, "poetry"},
		{[]string{"Pipfile", "Pipfile.lock"}, "pipenv"},
		{[]string{"requirements.txt", "setup.py", "setup.cfg"}, "pip"},
		{[]string{"Cargo.toml"}, "cargo"},
		{[]string{"build.gradle", "build.gradle.kts"}, "gradle"},
		{[]string{"pom.xml"}, "maven"},
		{[]string{"Gemfile", "Gemfile.lock"}, "bundler"},
		{[]string{"composer.json", "composer.lock"}, "composer"},
	}

	for _, check := range checks {
		for _, file := range check.files {
			if _, err := os.Stat(filepath.Join(workspacePath, file)); err == nil {
				return check.manager
			}
		}
	}

	return ""
}

// detectFramework identifies the framework from package.json, go.mod, etc.
func detectFramework(workspacePath, packageManager string) string {
	switch packageManager {
	case "npm", "yarn", "pnpm", "bun":
		return detectJSFramework(workspacePath)
	case "go":
		return detectGoFramework(workspacePath)
	case "pip", "pipenv", "poetry":
		return detectPythonFramework(workspacePath)
	case "cargo":
		return detectRustFramework(workspacePath)
	case "gradle", "maven":
		return detectJavaFramework(workspacePath)
	case "bundler":
		return detectRubyFramework(workspacePath)
	case "composer":
		return detectPHPFramework(workspacePath)
	}
	return ""
}

// detectJSFramework detects JavaScript/TypeScript frameworks from package.json.
func detectJSFramework(workspacePath string) string {
	data, err := os.ReadFile(filepath.Join(workspacePath, "package.json"))
	if err != nil {
		return ""
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}

	// Check all dependencies (both regular and dev)
	allDeps := make(map[string]struct{})
	for dep := range pkg.Dependencies {
		allDeps[dep] = struct{}{}
	}
	for dep := range pkg.DevDependencies {
		allDeps[dep] = struct{}{}
	}

	frameworks := []struct {
		dep  string
		name string
	}{
		{"next", "Next.js"},
		{"react", "React"},
		{"vue", "Vue"},
		{"nuxt", "Nuxt"},
		{"svelte", "Svelte"},
		{"sveltekit", "SvelteKit"},
		{"@angular/core", "Angular"},
		{"astro", "Astro"},
		{"remix", "Remix"},
		{"gatsby", "Gatsby"},
		{"express", "Express"},
		{"koa", "Koa"},
		{"fastify", "Fastify"},
		{"hapi", "Hapi"},
		{"nest", "NestJS"},
		{"electron", "Electron"},
		{"react-native", "React Native"},
		{"expo", "Expo"},
		{"@redwoodjs/core", "RedwoodJS"},
		{"solid-js", "SolidJS"},
		{"qwik", "Qwik"},
	}

	for _, fw := range frameworks {
		if _, ok := allDeps[fw.dep]; ok {
			return fw.name
		}
	}

	return ""
}

// detectGoFramework detects Go web frameworks from go.mod.
func detectGoFramework(workspacePath string) string {
	data, err := os.ReadFile(filepath.Join(workspacePath, "go.mod"))
	if err != nil {
		return ""
	}
	content := string(data)

	frameworks := []struct {
		importPath string
		name       string
	}{
		{"github.com/gin-gonic/gin", "Gin"},
		{"github.com/labstack/echo", "Echo"},
		{"github.com/go-chi/chi", "Chi"},
		{"github.com/gofiber/fiber", "Fiber"},
		{"github.com/gorilla/mux", "Gorilla Mux"},
		{"github.com/gorilla/websocket", "Gorilla WebSocket"},
		{"github.com/gin-contrib", "Gin"},
		{"github.com/beego", "Beego"},
		{"github.com/gogf/gf", "GoFrame"},
		{"github.com/go-kit/kit", "Go Kit"},
		{"github.com/kataras/iris", "Iris"},
		{"github.com/valyala/fasthttp", "fasthttp"},
	}

	for _, fw := range frameworks {
		if strings.Contains(content, fw.importPath) {
			return fw.name
		}
	}

	return ""
}

// detectPythonFramework detects Python web frameworks.
func detectPythonFramework(workspacePath string) string {
	// Check requirements.txt
	data, err := os.ReadFile(filepath.Join(workspacePath, "requirements.txt"))
	if err == nil {
		content := string(data)
		frameworks := []struct {
			dep  string
			name string
		}{
			{"django", "Django"},
			{"flask", "Flask"},
			{"fastapi", "FastAPI"},
			{"tornado", "Tornado"},
			{"bottle", "Bottle"},
			{"pyramid", "Pyramid"},
			{"starlette", "Starlette"},
			{"quart", "Quart"},
			{"falcon", "Falcon"},
		}
		for _, fw := range frameworks {
			if strings.Contains(content, fw.dep) {
				return fw.name
			}
		}
	}

	// Check pyproject.toml
	data, err = os.ReadFile(filepath.Join(workspacePath, "pyproject.toml"))
	if err == nil {
		content := string(data)
		if strings.Contains(content, "django") {
			return "Django"
		}
		if strings.Contains(content, "flask") {
			return "Flask"
		}
		if strings.Contains(content, "fastapi") {
			return "FastAPI"
		}
		if strings.Contains(content, "starlette") {
			return "Starlette"
		}
	}

	return ""
}

// detectRustFramework detects Rust web frameworks from Cargo.toml.
func detectRustFramework(workspacePath string) string {
	data, err := os.ReadFile(filepath.Join(workspacePath, "Cargo.toml"))
	if err != nil {
		return ""
	}
	content := string(data)

	frameworks := []struct {
		dep  string
		name string
	}{
		{"actix-web", "Actix Web"},
		{"axum", "Axum"},
		{"rocket", "Rocket"},
		{"tide", "Tide"},
		{"warp", "Warp"},
		{"salvo", "Salvo"},
		{"hyper", "Hyper"},
	}

	for _, fw := range frameworks {
		if strings.Contains(content, fw.dep) {
			return fw.name
		}
	}

	return ""
}

// detectJavaFramework detects Java web frameworks from build files.
func detectJavaFramework(workspacePath string) string {
	// Check pom.xml
	data, err := os.ReadFile(filepath.Join(workspacePath, "pom.xml"))
	if err == nil {
		content := string(data)
		if strings.Contains(content, "spring-boot") {
			return "Spring Boot"
		}
		if strings.Contains(content, "quarkus") {
			return "Quarkus"
		}
		if strings.Contains(content, "micronaut") {
			return "Micronaut"
		}
	}

	// Check build.gradle
	for _, filename := range []string{"build.gradle", "build.gradle.kts"} {
		data, err := os.ReadFile(filepath.Join(workspacePath, filename))
		if err == nil {
			content := string(data)
			if strings.Contains(content, "spring-boot") {
				return "Spring Boot"
			}
			if strings.Contains(content, "quarkus") {
				return "Quarkus"
			}
			if strings.Contains(content, "ktor") {
				return "Ktor"
			}
			if strings.Contains(content, "micronaut") {
				return "Micronaut"
			}
			if strings.Contains(content, "javalin") {
				return "Javalin"
			}
		}
	}

	return ""
}

// detectRubyFramework detects Ruby web frameworks from Gemfile.
func detectRubyFramework(workspacePath string) string {
	data, err := os.ReadFile(filepath.Join(workspacePath, "Gemfile"))
	if err != nil {
		return ""
	}
	content := string(data)

	if strings.Contains(content, "rails") {
		return "Rails"
	}
	if strings.Contains(content, "sinatra") {
		return "Sinatra"
	}
	if strings.Contains(content, "hanami") {
		return "Hanami"
	}
	if strings.Contains(content, "roda") {
		return "Roda"
	}
	if strings.Contains(content, "cuba") {
		return "Cuba"
	}
	if strings.Contains(content, "grape") {
		return "Grape"
	}

	return ""
}

// detectPHPFramework detects PHP web frameworks from composer.json.
func detectPHPFramework(workspacePath string) string {
	data, err := os.ReadFile(filepath.Join(workspacePath, "composer.json"))
	if err != nil {
		return ""
	}

	var composer struct {
		Require map[string]string `json:"require"`
	}
	if err := json.Unmarshal(data, &composer); err != nil {
		return ""
	}

	frameworks := map[string]string{
		"laravel/framework":       "Laravel",
		"symfony/framework-bundle": "Symfony",
		"cakephp/cakephp":          "CakePHP",
		"slim/slim":                "Slim",
		"codeigniter/framework":    "CodeIgniter",
		"laminas/laminas-mvc":      "Laminas",
		"phalcon/cphalcon":         "Phalcon",
		"yiisoft/yii2":             "Yii",
	}

	for dep, name := range frameworks {
		if _, ok := composer.Require[dep]; ok {
			return name
		}
	}

	return ""
}

// detectCommands extracts commands from package.json or other config files.
func detectCommands(workspacePath, packageManager string) (test, lint, typecheck, dev, build string) {
	switch packageManager {
	case "npm", "yarn", "pnpm", "bun":
		return detectJSCommands(workspacePath)
	case "go":
		return detectGoCommands(workspacePath)
	case "pip", "pipenv", "poetry":
		return detectPythonCommands(workspacePath)
	case "cargo":
		return detectRustCommands(workspacePath)
	case "gradle", "maven":
		return detectJavaCommands(workspacePath, packageManager)
	case "bundler":
		return detectRubyCommands(workspacePath)
	case "composer":
		return detectPHPCommands(workspacePath)
	default:
		return "", "", "", "", ""
	}
}

// detectJSCommands extracts scripts from package.json.
func detectJSCommands(workspacePath string) (test, lint, typecheck, dev, build string) {
	data, err := os.ReadFile(filepath.Join(workspacePath, "package.json"))
	if err != nil {
		return "", "", "", "", ""
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", "", "", "", ""
	}

	scripts := pkg.Scripts

	// Test command
	for _, key := range []string{"test", "test:ci", "test:unit", "test:e2e", "jest", "vitest"} {
		if v, ok := scripts[key]; ok {
			test = v
			break
		}
	}

	// Lint command
	for _, key := range []string{"lint", "lint:check", "lint:ci", "eslint", "biome:check"} {
		if v, ok := scripts[key]; ok {
			lint = v
			break
		}
	}

	// Typecheck command
	for _, key := range []string{"typecheck", "type-check", "tsc", "tsc:check", "ts:check"} {
		if v, ok := scripts[key]; ok {
			typecheck = v
			break
		}
	}

	// Dev command
	for _, key := range []string{"dev", "start:dev", "develop", "serve", "watch"} {
		if v, ok := scripts[key]; ok {
			dev = v
			break
		}
	}

	// Build command
	for _, key := range []string{"build", "build:prod", "build:app", "compile"} {
		if v, ok := scripts[key]; ok {
			build = v
			break
		}
	}

	return test, lint, typecheck, dev, build
}

// detectGoCommands infers Go commands.
func detectGoCommands(workspacePath string) (test, lint, typecheck, dev, build string) {
	test = "go test ./..."
	build = "go build"

	// Check for common Go tools
	if _, err := os.Stat(filepath.Join(workspacePath, "Makefile")); err == nil {
		return detectMakefileCommands(workspacePath)
	}
	if _, err := os.Stat(filepath.Join(workspacePath, "Taskfile.yml")); err == nil {
		return detectTaskfileCommands(workspacePath)
	}

	// Check for golangci-lint config
	for _, f := range []string{".golangci.yml", ".golangci.yaml", ".golangci.toml"} {
		if _, err := os.Stat(filepath.Join(workspacePath, f)); err == nil {
			lint = "golangci-lint run"
			break
		}
	}

	return test, lint, "", dev, build
}

// detectPythonCommands infers Python commands.
func detectPythonCommands(workspacePath string) (test, lint, typecheck, dev, build string) {
	// Check for pytest.ini, setup.cfg, pyproject.toml
	if _, err := os.Stat(filepath.Join(workspacePath, "pytest.ini")); err == nil {
		test = "pytest"
	} else if _, err := os.Stat(filepath.Join(workspacePath, "setup.cfg")); err == nil {
		test = "pytest"
	} else {
		test = "python -m pytest"
	}

	// Check for common lint tools config
	for _, f := range []string{".flake8", "setup.cfg", "pyproject.toml"} {
		if _, err := os.Stat(filepath.Join(workspacePath, f)); err == nil {
			lint = "flake8"
			break
		}
	}

	// Check for mypy config
	for _, f := range []string{"mypy.ini", "setup.cfg", "pyproject.toml"} {
		if _, err := os.Stat(filepath.Join(workspacePath, f)); err == nil {
			typecheck = "mypy"
			break
		}
	}

	// Check for Makefile
	if _, err := os.Stat(filepath.Join(workspacePath, "Makefile")); err == nil {
		return detectMakefileCommands(workspacePath)
	}

	return test, lint, typecheck, dev, build
}

// detectRustCommands infers Rust commands from Cargo.toml.
func detectRustCommands(workspacePath string) (test, lint, typecheck, dev, build string) {
	test = "cargo test"
	build = "cargo build --release"

	// Check for clippy config or just use it
	lint = "cargo clippy"
	typecheck = "cargo check"

	return test, lint, typecheck, dev, build
}

// detectJavaCommands infers Java commands.
func detectJavaCommands(workspacePath string, packageManager string) (test, lint, typecheck, dev, build string) {
	if packageManager == "gradle" {
		test = "./gradlew test"
		build = "./gradlew build"
	} else {
		test = "mvn test"
		build = "mvn package"
	}

	return test, lint, typecheck, dev, build
}

// detectRubyCommands infers Ruby commands.
func detectRubyCommands(workspacePath string) (test, lint, typecheck, dev, build string) {
	// Check for Rakefile
	if _, err := os.Stat(filepath.Join(workspacePath, "Rakefile")); err == nil {
		test = "bundle exec rake"
	} else {
		test = "bundle exec rspec"
	}

	// Check for rubocop
	if _, err := os.Stat(filepath.Join(workspacePath, ".rubocop.yml")); err == nil {
		lint = "bundle exec rubocop"
	}

	return test, lint, typecheck, dev, build
}

// detectPHPCommands infers PHP commands.
func detectPHPCommands(workspacePath string) (test, lint, typecheck, dev, build string) {
	// Check for phpunit
	if _, err := os.Stat(filepath.Join(workspacePath, "phpunit.xml")); err == nil {
		test = "vendor/bin/phpunit"
	} else if _, err := os.Stat(filepath.Join(workspacePath, "phpunit.xml.dist")); err == nil {
		test = "vendor/bin/phpunit"
	}

	return test, lint, typecheck, dev, build
}

// detectMakefileCommands extracts commands from a Makefile.
func detectMakefileCommands(workspacePath string) (test, lint, typecheck, dev, build string) {
	data, err := os.ReadFile(filepath.Join(workspacePath, "Makefile"))
	if err != nil {
		return "", "", "", "", ""
	}
	content := string(data)

	if strings.Contains(content, "test:") {
		test = "make test"
	}
	if strings.Contains(content, "lint:") {
		lint = "make lint"
	}
	if strings.Contains(content, "typecheck:") {
		typecheck = "make typecheck"
	}
	if strings.Contains(content, "dev:") {
		dev = "make dev"
	}
	if strings.Contains(content, "build:") {
		build = "make build"
	}

	return test, lint, typecheck, dev, build
}

// detectTaskfileCommands extracts commands from a Taskfile.
func detectTaskfileCommands(workspacePath string) (test, lint, typecheck, dev, build string) {
	for _, filename := range []string{"Taskfile.yml", "Taskfile.yaml"} {
		data, err := os.ReadFile(filepath.Join(workspacePath, filename))
		if err != nil {
			continue
		}
		content := string(data)

		if strings.Contains(content, "test:") {
			test = "task test"
		}
		if strings.Contains(content, "lint:") {
			lint = "task lint"
		}
		if strings.Contains(content, "typecheck:") {
			typecheck = "task typecheck"
		}
		if strings.Contains(content, "dev:") {
			dev = "task dev"
		}
		if strings.Contains(content, "build:") {
			build = "task build"
		}
		return test, lint, typecheck, dev, build
	}

	return "", "", "", "", ""
}

// detectDockerfile checks for the presence of a Dockerfile in the workspace.
func detectDockerfile(workspacePath string) bool {
	for _, name := range []string{"Dockerfile", "dockerfile", "Dockerfile.prod", "Dockerfile.production"} {
		if _, err := os.Stat(filepath.Join(workspacePath, name)); err == nil {
			return true
		}
	}
	return false
}

// detectDevcontainer checks for the presence of a .devcontainer configuration.
func detectDevcontainer(workspacePath string) bool {
	entries, err := os.ReadDir(workspacePath)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() == ".devcontainer" {
			return true
		}
	}
	return false
}
