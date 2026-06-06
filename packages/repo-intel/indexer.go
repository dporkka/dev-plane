package repointel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrNotImplemented is kept for compatibility with older callers.
var ErrNotImplemented = fmt.Errorf("not implemented: will be available in Phase 2 with tree-sitter integration")

// Symbol represents a code symbol (function, type, variable, etc.) found in the repository.
type Symbol struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`     // function, struct, interface, variable, etc.
	Language   string `json:"language"` // go, typescript, python, etc.
	FilePath   string `json:"file_path"`
	LineStart  int    `json:"line_start"`
	LineEnd    int    `json:"line_end"`
	Definition string `json:"definition,omitempty"`
}

// Dependency represents a project dependency.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"` // npm, pypi, go_modules, etc.
	Type    string `json:"type"`   // production, development, peer
}

// SearchResult represents a single search result from the indexer.
type SearchResult struct {
	Symbol  Symbol  `json:"symbol"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

// IndexEntry represents a single indexed item in the repository.
type IndexEntry struct {
	FilePath string   `json:"file_path"`
	Language string   `json:"language"`
	Symbols  []Symbol `json:"symbols"`
	Content  string   `json:"content,omitempty"`
}

// RepoIndexer provides repository indexing and search capabilities.
type RepoIndexer interface {
	// Index scans the repository and builds the search index.
	Index(ctx context.Context, repoPath string) error

	// Search queries the index for matching symbols or content.
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)

	// GetSymbols returns all symbols in a given file.
	GetSymbols(ctx context.Context, filePath string) ([]Symbol, error)

	// GetDependencies extracts project dependencies.
	GetDependencies(ctx context.Context, repoPath string) ([]Dependency, error)
}

// StubIndexer is a lightweight lexical implementation of RepoIndexer.
type StubIndexer struct {
	baseDir string
	entries []IndexEntry
}

// NewStubIndexer creates a new lightweight indexer.
func NewStubIndexer(baseDir string) *StubIndexer {
	return &StubIndexer{
		baseDir: baseDir,
		entries: make([]IndexEntry, 0),
	}
}

// Index performs a best-effort source file scan.
func (i *StubIndexer) Index(ctx context.Context, repoPath string) error {
	i.entries = i.entries[:0] // clear

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip files we can't read
		}
		if info.IsDir() {
			// Skip common non-source directories
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" ||
				name == ".next" || name == "dist" || name == "build" ||
				name == "bin" || name == ".venv" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary and large files
		if info.Size() > 1024*1024 { // 1MB
			return nil
		}

		// Detect language from extension
		lang := detectLanguage(path)
		if lang == "" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(repoPath, path)
		content := string(data)
		i.entries = append(i.entries, IndexEntry{
			FilePath: relPath,
			Language: lang,
			Symbols:  extractSymbols(relPath, lang, content),
			Content:  content,
		})

		return nil
	})

	return err
}

// Search returns best-effort symbol, file, and content matches.
func (i *StubIndexer) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	query = strings.ToLower(query)
	var results []SearchResult

	for _, entry := range i.entries {
		if len(results) >= limit {
			break
		}
		for _, symbol := range entry.Symbols {
			if len(results) >= limit {
				break
			}
			if strings.Contains(strings.ToLower(symbol.Name), query) ||
				strings.Contains(strings.ToLower(symbol.Definition), query) {
				results = append(results, SearchResult{
					Symbol:  symbol,
					Score:   2.0,
					Snippet: symbol.Definition,
				})
			}
		}
		if len(results) >= limit {
			break
		}
		lowerPath := strings.ToLower(entry.FilePath)
		if strings.Contains(lowerPath, query) {
			results = append(results, SearchResult{
				Symbol: Symbol{
					Name:     filepath.Base(entry.FilePath),
					Kind:     "file",
					Language: entry.Language,
					FilePath: entry.FilePath,
				},
				Score: 1.0,
			})
			continue
		}
		if snippet := contentSnippet(entry.Content, query); snippet != "" {
			results = append(results, SearchResult{
				Symbol: Symbol{
					Name:     filepath.Base(entry.FilePath),
					Kind:     "file",
					Language: entry.Language,
					FilePath: entry.FilePath,
				},
				Score:   0.5,
				Snippet: snippet,
			})
		}
	}

	return results, nil
}

// GetSymbols returns symbols for a file.
func (i *StubIndexer) GetSymbols(ctx context.Context, filePath string) ([]Symbol, error) {
	cleanPath := filepath.Clean(filePath)
	for _, entry := range i.entries {
		if filepath.Clean(entry.FilePath) == cleanPath {
			return append([]Symbol(nil), entry.Symbols...), nil
		}
	}

	fullPath := filePath
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(i.baseDir, filePath)
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("read symbols from %q: %w", filePath, err)
	}
	lang := detectLanguage(fullPath)
	if lang == "" {
		return []Symbol{}, nil
	}
	return extractSymbols(filePath, lang, string(data)), nil
}

// GetDependencies extracts dependencies.
func (i *StubIndexer) GetDependencies(ctx context.Context, repoPath string) ([]Dependency, error) {
	var deps []Dependency

	// Try to read go.mod
	goModPath := filepath.Join(repoPath, "go.mod")
	if data, err := os.ReadFile(goModPath); err == nil {
		goDeps := parseGoMod(string(data))
		deps = append(deps, goDeps...)
	}

	// Try to read package.json
	pkgJSONPath := filepath.Join(repoPath, "package.json")
	if data, err := os.ReadFile(pkgJSONPath); err == nil {
		jsDeps := parsePackageJSON(data)
		deps = append(deps, jsDeps...)
	}

	return deps, nil
}

// EntryCount returns the number of indexed entries (for testing/debugging).
func (i *StubIndexer) EntryCount() int {
	return len(i.entries)
}

type symbolPattern struct {
	re        *regexp.Regexp
	kind      string
	nameIndex int
	kindIndex int
}

var symbolPatterns = map[string][]symbolPattern{
	"go": {
		{re: regexp.MustCompile(`^\s*func\s+(?:\([^)]+\)\s*)?([A-Za-z_][A-Za-z0-9_]*)\s*\(`), kind: "function", nameIndex: 1},
		{re: regexp.MustCompile(`^\s*type\s+([A-Za-z_][A-Za-z0-9_]*)\s+(struct|interface|[A-Za-z_][A-Za-z0-9_]*)`), nameIndex: 1, kindIndex: 2},
		{re: regexp.MustCompile(`^\s*(?:var|const)\s+([A-Za-z_][A-Za-z0-9_]*)\b`), kind: "variable", nameIndex: 1},
	},
	"typescript": javascriptSymbolPatterns(),
	"javascript": javascriptSymbolPatterns(),
	"python": {
		{re: regexp.MustCompile(`^\s*def\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`), kind: "function", nameIndex: 1},
		{re: regexp.MustCompile(`^\s*class\s+([A-Za-z_][A-Za-z0-9_]*)\b`), kind: "class", nameIndex: 1},
	},
	"rust": {
		{re: regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`), kind: "function", nameIndex: 1},
		{re: regexp.MustCompile(`^\s*(?:pub\s+)?(struct|enum|trait)\s+([A-Za-z_][A-Za-z0-9_]*)\b`), nameIndex: 2, kindIndex: 1},
	},
	"java": {
		{re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?(class|interface|enum)\s+([A-Za-z_][A-Za-z0-9_]*)\b`), nameIndex: 2, kindIndex: 1},
		{re: regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?(?:static\s+)?[A-Za-z_][A-Za-z0-9_<>\[\]]*\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`), kind: "method", nameIndex: 1},
	},
}

func javascriptSymbolPatterns() []symbolPattern {
	return []symbolPattern{
		{re: regexp.MustCompile(`^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`), kind: "function", nameIndex: 1},
		{re: regexp.MustCompile(`^\s*(?:export\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`), kind: "class", nameIndex: 1},
		{re: regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`), kind: "interface", nameIndex: 1},
		{re: regexp.MustCompile(`^\s*(?:export\s+)?type\s+([A-Za-z_$][A-Za-z0-9_$]*)\b`), kind: "type", nameIndex: 1},
		{re: regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=`), kind: "variable", nameIndex: 1},
	}
}

func extractSymbols(filePath, language, content string) []Symbol {
	patterns := symbolPatterns[language]
	if len(patterns) == 0 {
		return []Symbol{}
	}

	var symbols []Symbol
	for lineNumber, line := range strings.Split(content, "\n") {
		for _, pattern := range patterns {
			matches := pattern.re.FindStringSubmatch(line)
			if len(matches) == 0 {
				continue
			}
			kind := pattern.kind
			if pattern.kindIndex > 0 && pattern.kindIndex < len(matches) {
				kind = matches[pattern.kindIndex]
			}
			symbols = append(symbols, Symbol{
				Name:       matches[pattern.nameIndex],
				Kind:       kind,
				Language:   language,
				FilePath:   filePath,
				LineStart:  lineNumber + 1,
				LineEnd:    lineNumber + 1,
				Definition: strings.TrimSpace(line),
			})
			break
		}
	}
	return symbols
}

func contentSnippet(content, query string) string {
	query = strings.ToLower(query)
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.ToLower(line), query) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// detectLanguage maps file extensions to language names.
func detectLanguage(path string) string {
	switch filepath.Ext(path) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc":
		return "cpp"
	case ".sql":
		return "sql"
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	case ".md":
		return "markdown"
	case ".dockerfile", ".Dockerfile":
		return "dockerfile"
	case ".tf":
		return "terraform"
	case ".sh":
		return "shell"
	default:
		return ""
	}
}

// parseGoMod extracts dependencies from go.mod content.
func parseGoMod(content string) []Dependency {
	var deps []Dependency
	inRequire := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.Split(line, "//")[0])
		if line == "" {
			continue
		}
		if line == "require (" {
			inRequire = true
			continue
		}
		if inRequire && line == ")" {
			inRequire = false
			continue
		}
		if strings.HasPrefix(line, "require ") || inRequire {
			line = strings.TrimPrefix(line, "require ")
			line = strings.TrimSpace(line)
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				deps = append(deps, Dependency{
					Name:    parts[0],
					Version: parts[1],
					Source:  "go_modules",
					Type:    "production",
				})
			}
		}
	}
	return deps
}

// parsePackageJSON extracts dependencies from package.json bytes.
func parsePackageJSON(data []byte) []Dependency {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	var deps []Dependency
	for name, version := range pkg.Dependencies {
		deps = append(deps, Dependency{
			Name:    name,
			Version: version,
			Source:  "npm",
			Type:    "production",
		})
	}
	for name, version := range pkg.DevDependencies {
		deps = append(deps, Dependency{
			Name:    name,
			Version: version,
			Source:  "npm",
			Type:    "development",
		})
	}
	return deps
}

var _ RepoIndexer = (*StubIndexer)(nil)
