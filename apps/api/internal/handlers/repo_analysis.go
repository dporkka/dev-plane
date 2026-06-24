package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/ai-dev-control-plane/api/internal/authz"
	repointel "github.com/ai-dev-control-plane/repo-intel"

	"github.com/ai-dev-control-plane/api/internal/respond"
)

// RepoAnalysis represents the analysis result for a repository.
type RepoAnalysis struct {
	RepositoryID    string           `json:"repository_id"`
	Languages       []RepoLanguage   `json:"languages"`
	PackageManagers []string         `json:"package_managers"`
	Frameworks      []string         `json:"frameworks"`
	TestCommands    []string         `json:"test_commands"`
	BuildCommands   []string         `json:"build_commands"`
	Dependencies    []RepoDependency `json:"dependencies,omitempty"`
	EntryPoints     []string         `json:"entry_points"`
	HasDockerfile   bool             `json:"has_dockerfile"`
	HasCIConfig     bool             `json:"has_ci_config"`
	Structure       []DirEntry       `json:"structure"`
	AnalyzedAt      string           `json:"analyzed_at"`
}

// RepoLanguage represents a language found in the repository with file count.
type RepoLanguage struct {
	Name      string `json:"name"`
	FileCount int    `json:"file_count"`
}

// RepoDependency represents a detected dependency.
type RepoDependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
	Type    string `json:"type"`
}

// DirEntry represents a top-level directory entry.
type DirEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
}

// AnalyzeRepo triggers and returns an analysis of a repository's structure,
// package managers, frameworks, and test commands.
func (h *Handler) AnalyzeRepo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, ok := authz.RequireUser(w, r)
	if !ok {
		return
	}

	repoID := chi.URLParam(r, "id")
	if repoID == "" {
		respond.Error(w, http.StatusBadRequest, errors.New("repository id is required"))
		return
	}

	if err := authz.AuthorizeRepository(ctx, h.db, user, repoID); err != nil {
		respond.Error(w, http.StatusNotFound, errors.New("repository not found"))
		return
	}

	// Get repository details
	var repo struct {
		ID       string
		CloneURL string
		FullName string
		Owner    string
		Name     string
	}
	err := h.db.QueryRowContext(ctx, `
		SELECT id, clone_url, full_name, owner, name
		FROM repositories WHERE id = $1 AND deleted_at IS NULL
	`, repoID).Scan(&repo.ID, &repo.CloneURL, &repo.FullName, &repo.Owner, &repo.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respond.Error(w, http.StatusNotFound, errors.New("repository not found"))
			return
		}
		respond.Error(w, http.StatusInternalServerError, err)
		return
	}

	// Determine the local repo path
	// Check if there's a local clone, otherwise use a computed path
	repoDir := filepath.Join(h.getReposBaseDir(), repo.Owner, repo.Name)

	// If local clone doesn't exist, return a response indicating we need to clone first
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); os.IsNotExist(err) {
		respond.JSON(w, http.StatusAccepted, map[string]interface{}{
			"repository_id": repoID,
			"status":        "pending_clone",
			"message":       "Repository needs to be cloned before analysis",
			"clone_url":     repo.CloneURL,
		})
		return
	}

	// Perform analysis using the repo-intel package
	analysis, err := performRepoAnalysis(ctx, repoDir, repoID)
	if err != nil {
		h.logger.Warn("repo analysis failed, returning partial results", "error", err)
		// Return basic structure analysis even if full analysis fails
		analysis = fallbackAnalysis(repoDir, repoID)
	}

	respond.JSON(w, http.StatusOK, analysis)
}

// performRepoAnalysis runs the full repository analysis using repointel.
func performRepoAnalysis(ctx context.Context, repoDir, repoID string) (*RepoAnalysis, error) {
	analysis := &RepoAnalysis{
		RepositoryID:    repoID,
		Languages:       []RepoLanguage{},
		PackageManagers: []string{},
		Frameworks:      []string{},
		TestCommands:    []string{},
		BuildCommands:   []string{},
		Dependencies:    []RepoDependency{},
		EntryPoints:     []string{},
		Structure:       []DirEntry{},
		AnalyzedAt:      "",
	}

	// Use the lightweight indexer to scan the repo.
	indexer := repointel.NewStubIndexer(repoDir)

	// Walk and index the repository
	if err := indexer.Index(ctx, repoDir); err != nil {
		return nil, fmt.Errorf("index repository: %w", err)
	}

	langCounts := make(map[string]int)

	// Walk the directory ourselves for language stats and structure
	err := filepath.Walk(repoDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip
		}

		relPath, _ := filepath.Rel(repoDir, path)
		if relPath == "." {
			return nil
		}

		// Count top-level entries for structure
		if filepath.Dir(relPath) == "." {
			analysis.Structure = append(analysis.Structure, DirEntry{
				Name:  info.Name(),
				IsDir: info.IsDir(),
			})
		}

		// Skip hidden and common non-source directories
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" ||
				name == ".next" || name == "dist" || name == "build" ||
				name == "bin" || name == ".venv" || name == "__pycache__" ||
				name == ".github" || name == ".vscode" || name == ".idea" {
				return filepath.SkipDir
			}
			return nil
		}

		// Detect language
		lang := detectLanguage(info.Name())
		if lang != "" {
			langCounts[lang]++
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Convert language counts to sorted list
	for lang, count := range langCounts {
		analysis.Languages = append(analysis.Languages, RepoLanguage{
			Name:      lang,
			FileCount: count,
		})
	}

	// Detect package managers
	analysis.PackageManagers = detectPackageManagers(repoDir)
	analysis.Frameworks = detectFrameworks(repoDir)
	analysis.TestCommands = suggestTestCommands(repoDir, analysis.PackageManagers)
	analysis.BuildCommands = suggestBuildCommands(repoDir, analysis.PackageManagers)

	// Detect entry points
	analysis.EntryPoints = detectEntryPoints(repoDir)

	// Check for Dockerfile and CI config
	analysis.HasDockerfile = fileExists(repoDir, "Dockerfile")
	if !analysis.HasDockerfile {
		// Check for dockerfile with different case
		analysis.HasDockerfile = fileExistsCaseInsensitive(repoDir, "dockerfile")
	}
	analysis.HasCIConfig = dirExists(repoDir, ".github") || dirExists(repoDir, ".gitlab-ci.yml")

	// Try to get dependencies
	deps, err := indexer.GetDependencies(ctx, repoDir)
	if err == nil {
		for _, dep := range deps {
			analysis.Dependencies = append(analysis.Dependencies, RepoDependency{
				Name:    dep.Name,
				Version: dep.Version,
				Source:  dep.Source,
				Type:    dep.Type,
			})
		}
	}

	return analysis, nil
}

// detectPackageManagers looks for known package manager files.
func detectPackageManagers(repoDir string) []string {
	var managers []string
	if fileExists(repoDir, "go.mod") {
		managers = append(managers, "go_modules")
	}
	if fileExists(repoDir, "package.json") {
		managers = append(managers, "npm")
	}
	if fileExists(repoDir, "package-lock.json") {
		if !containsString(managers, "npm") {
			managers = append(managers, "npm")
		}
	}
	if fileExists(repoDir, "yarn.lock") {
		managers = append(managers, "yarn")
	}
	if fileExists(repoDir, "pnpm-lock.yaml") {
		managers = append(managers, "pnpm")
	}
	if fileExists(repoDir, "requirements.txt") {
		managers = append(managers, "pip")
	}
	if fileExists(repoDir, "poetry.lock") {
		managers = append(managers, "poetry")
	}
	if fileExists(repoDir, "Cargo.toml") {
		managers = append(managers, "cargo")
	}
	if fileExists(repoDir, "pom.xml") {
		managers = append(managers, "maven")
	}
	if fileExists(repoDir, "build.gradle") {
		managers = append(managers, "gradle")
	}
	if fileExists(repoDir, "composer.json") {
		managers = append(managers, "composer")
	}
	if fileExists(repoDir, "Gemfile") {
		managers = append(managers, "bundler")
	}
	return managers
}

// detectFrameworks looks for framework indicators in the repository.
func detectFrameworks(repoDir string) []string {
	var frameworks []string

	// Check package.json for JS/TS frameworks
	pkgJSONPath := filepath.Join(repoDir, "package.json")
	if data, err := os.ReadFile(pkgJSONPath); err == nil {
		var pkg struct {
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			frameworkIndicators := map[string]string{
				"react":         "React",
				"vue":           "Vue",
				"@angular/core": "Angular",
				"next":          "Next.js",
				"nuxt":          "Nuxt",
				"svelte":        "Svelte",
				"express":       "Express",
				"@nestjs/core":  "NestJS",
				"fastify":       "Fastify",
				"hono":          "Hono",
				"astro":         "Astro",
				"remix":         "Remix",
			}
			for dep, name := range frameworkIndicators {
				if _, ok := pkg.Dependencies[dep]; ok {
					frameworks = append(frameworks, name)
				}
				if _, ok := pkg.DevDependencies[dep]; ok {
					if !containsString(frameworks, name) {
						frameworks = append(frameworks, name)
					}
				}
			}
		}
	}

	// Check go.mod for Go frameworks
	goModPath := filepath.Join(repoDir, "go.mod")
	if data, err := os.ReadFile(goModPath); err == nil {
		content := string(data)
		goFrameworks := map[string]string{
			"github.com/gin-gonic/gin": "Gin",
			"github.com/gofiber/fiber": "Fiber",
			"github.com/labstack/echo": "Echo",
			"github.com/gorilla/mux":   "Gorilla Mux",
			"net/http":                 "net/http",
			"google.golang.org/grpc":   "gRPC",
		}
		for importPath, name := range goFrameworks {
			if strings.Contains(content, importPath) {
				frameworks = append(frameworks, name)
			}
		}
	}

	// Check for Python frameworks
	if fileExists(repoDir, "requirements.txt") {
		if data, err := os.ReadFile(filepath.Join(repoDir, "requirements.txt")); err == nil {
			content := string(data)
			pythonFrameworks := map[string]string{
				"django":     "Django",
				"flask":      "Flask",
				"fastapi":    "FastAPI",
				"tornado":    "Tornado",
				"pytorch":    "PyTorch",
				"tensorflow": "TensorFlow",
			}
			for indicator, name := range pythonFrameworks {
				if strings.Contains(content, indicator) {
					frameworks = append(frameworks, name)
				}
			}
		}
	}

	return frameworks
}

// suggestTestCommands suggests test commands based on package managers.
func suggestTestCommands(repoDir string, pkgManagers []string) []string {
	var commands []string
	for _, pm := range pkgManagers {
		switch pm {
		case "npm", "yarn", "pnpm":
			commands = append(commands, "npm test")
		case "go_modules":
			commands = append(commands, "go test ./...")
		case "pip", "poetry":
			commands = append(commands, "pytest", "python -m unittest discover")
		case "cargo":
			commands = append(commands, "cargo test")
		case "maven":
			commands = append(commands, "mvn test")
		case "gradle":
			commands = append(commands, "./gradlew test")
		}
	}
	return commands
}

// suggestBuildCommands suggests build commands based on package managers.
func suggestBuildCommands(repoDir string, pkgManagers []string) []string {
	var commands []string
	for _, pm := range pkgManagers {
		switch pm {
		case "npm", "yarn", "pnpm":
			commands = append(commands, "npm run build")
		case "go_modules":
			commands = append(commands, "go build ./...")
		case "pip", "poetry":
			// Python typically doesn't have a build step
		case "cargo":
			commands = append(commands, "cargo build")
		case "maven":
			commands = append(commands, "mvn package")
		case "gradle":
			commands = append(commands, "./gradlew build")
		}
	}
	return commands
}

// detectEntryPoints looks for common entry point files.
func detectEntryPoints(repoDir string) []string {
	var entryPoints []string
	candidates := []string{
		"main.go", "cmd/main.go", "cmd/server/main.go",
		"index.js", "src/index.js", "src/main.js",
		"index.ts", "src/index.ts", "src/main.ts",
		"app.py", "manage.py", "wsgi.py",
		"lib/main.dart",
		"src/main.rs", "main.rs",
	}
	for _, candidate := range candidates {
		if fileExists(repoDir, candidate) {
			entryPoints = append(entryPoints, candidate)
		}
	}
	return entryPoints
}

// fileExists checks if a file exists in the repo directory.
func fileExists(repoDir, relativePath string) bool {
	_, err := os.Stat(filepath.Join(repoDir, relativePath))
	return err == nil
}

// fileExistsCaseInsensitive checks for a file case-insensitively.
func fileExistsCaseInsensitive(dir, name string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			if filepath.Ext(strings.ToLower(entry.Name())) == "."+strings.ToLower(name) {
				return true
			}
		}
	}
	return false
}

// dirExists checks if a directory exists in the repo directory.
func dirExists(repoDir, relativePath string) bool {
	info, err := os.Stat(filepath.Join(repoDir, relativePath))
	return err == nil && info.IsDir()
}

// fallbackAnalysis provides a basic analysis when full analysis fails.
func fallbackAnalysis(repoDir, repoID string) *RepoAnalysis {
	analysis := &RepoAnalysis{
		RepositoryID:    repoID,
		Languages:       []RepoLanguage{},
		PackageManagers: []string{},
		Frameworks:      []string{},
		TestCommands:    []string{},
		BuildCommands:   []string{},
		EntryPoints:     []string{},
		Structure:       []DirEntry{},
	}

	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return analysis
	}
	for _, entry := range entries {
		analysis.Structure = append(analysis.Structure, DirEntry{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
		})
	}
	analysis.PackageManagers = detectPackageManagers(repoDir)
	analysis.TestCommands = suggestTestCommands(repoDir, analysis.PackageManagers)
	return analysis
}

func detectLanguage(filename string) string {
	if filename == "Gemfile" {
		return "Ruby"
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".go":
		return "Go"
	case ".js", ".jsx":
		return "JavaScript"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".py":
		return "Python"
	case ".java":
		return "Java"
	case ".rb":
		return "Ruby"
	case ".rs":
		return "Rust"
	case ".php":
		return "PHP"
	case ".cs":
		return "C#"
	case ".cpp", ".cc", ".cxx", ".hpp", ".h":
		return "C/C++"
	case ".sh":
		return "Shell"
	case ".sql":
		return "SQL"
	default:
		return ""
	}
}

// containsString checks if a string slice contains a value.
func containsString(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}

// getReposBaseDir returns the base directory where repositories are cloned.
func (h *Handler) getReposBaseDir() string {
	if dir := os.Getenv("REPOS_BASE_DIR"); dir != "" {
		return dir
	}
	return "./repos"
}
