package agentrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/projectbrain"
	repointel "github.com/ai-dev-control-plane/repo-intel"
)

const repoIntelSearchLimitPerTerm = 6

// RepoIntelContextSourceFactory builds a first-party lexical repository source
// for the concrete local workspace used by a run.
type RepoIntelContextSourceFactory struct{}

func NewRepoIntelContextSourceFactory() *RepoIntelContextSourceFactory {
	return &RepoIntelContextSourceFactory{}
}

func (*RepoIntelContextSourceFactory) SourceForWorkspace(workspacePath string) projectbrain.Source {
	return NewRepoIntelContextSource(workspacePath)
}

// RepoIntelContextSource adapts the existing permissive first-party repo-intel
// indexer into Project Brain facts. It fails closed unless the complete working
// tree exactly matches the requested immutable commit.
type RepoIntelContextSource struct {
	workspacePath string
}

func NewRepoIntelContextSource(workspacePath string) *RepoIntelContextSource {
	return &RepoIntelContextSource{workspacePath: strings.TrimSpace(workspacePath)}
}

func (*RepoIntelContextSource) Name() string { return "repo-intel" }

func (s *RepoIntelContextSource) Facts(ctx context.Context, query projectbrain.Query) ([]projectbrain.Fact, error) {
	if s == nil || s.workspacePath == "" {
		return nil, errors.New("repo-intel workspace path is required")
	}
	if strings.TrimSpace(query.Repository) == "" {
		return nil, errors.New("repo-intel repository is required")
	}
	if err := verifyRepoIntelRevision(ctx, s.workspacePath, query.Revision); err != nil {
		return nil, err
	}

	indexer := repointel.NewStubIndexer(s.workspacePath)
	if err := indexer.Index(ctx, s.workspacePath); err != nil {
		return nil, fmt.Errorf("index repo-intel workspace: %w", err)
	}

	observedAt := time.Now().UTC()
	facts := make([]projectbrain.Fact, 0)
	seen := make(map[string]struct{})
	appendSymbol := func(symbol repointel.Symbol) {
		name := strings.TrimSpace(symbol.Name)
		path := filepath.ToSlash(strings.TrimSpace(symbol.FilePath))
		if name == "" || path == "" || symbol.Kind == "file" {
			return
		}
		location := path
		if symbol.LineStart > 0 {
			location += ":" + strconv.Itoa(symbol.LineStart)
		}
		key := name + "\x00" + location
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		facts = append(facts, projectbrain.Fact{
			Subject:    "symbol:" + name,
			Relation:   "defined_in",
			Object:     location,
			Repository: query.Repository,
			Revision:   query.Revision,
			Scope:      projectbrain.ScopeRevision,
			Provenance: []projectbrain.Provenance{{
				Source:     "repo-intel",
				Reference:  path + "#L" + strconv.Itoa(maxInt(symbol.LineStart, 1)),
				ObservedAt: observedAt,
				Confidence: 0.75,
			}},
		})
	}

	for _, changedPath := range query.ChangedPaths {
		changedPath, err := safeRepoIntelChangedPath(s.workspacePath, changedPath)
		if err != nil {
			return nil, err
		}
		if changedPath == "" {
			continue
		}
		symbols, err := indexer.GetSymbols(ctx, changedPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("repo-intel symbols for %s: %w", changedPath, err)
		}
		for _, symbol := range symbols {
			appendSymbol(symbol)
		}
	}

	for _, term := range query.Terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		results, err := indexer.Search(ctx, term, repoIntelSearchLimitPerTerm)
		if err != nil {
			return nil, fmt.Errorf("repo-intel search %q: %w", term, err)
		}
		for _, result := range results {
			appendSymbol(result.Symbol)
		}
	}

	return facts, nil
}

func safeRepoIntelChangedPath(workspacePath, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("repo-intel changed path escapes workspace: %q", value)
	}

	root, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", fmt.Errorf("resolve repo-intel workspace: %w", err)
	}
	full := filepath.Join(root, clean)
	info, err := os.Lstat(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return filepath.ToSlash(clean), nil
		}
		return "", fmt.Errorf("inspect repo-intel changed path %q: %w", value, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("repo-intel changed path is a symlink: %q", value)
	}

	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repo-intel workspace symlinks: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", fmt.Errorf("resolve repo-intel changed path %q: %w", value, err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("repo-intel changed path escapes workspace: %q", value)
	}
	return filepath.ToSlash(clean), nil
}

func verifyRepoIntelRevision(ctx context.Context, workspacePath, revision string) error {
	requested := strings.TrimSpace(revision)
	if !strings.HasPrefix(requested, "git-commit:") {
		return fmt.Errorf("repo-intel source requires git-commit revision, got %q", requested)
	}
	requestedHash := strings.TrimSpace(strings.TrimPrefix(requested, "git-commit:"))
	if requestedHash == "" {
		return errors.New("repo-intel git-commit revision is empty")
	}

	runGit := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = workspacePath
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
		return strings.TrimSpace(string(output)), nil
	}

	head, err := runGit("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if !strings.EqualFold(head, requestedHash) {
		return fmt.Errorf("repo-intel workspace HEAD %s does not match requested commit %s", head, requestedHash)
	}
	headTree, err := runGit("rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	workingTree, err := localSubjectRevision(ctx, workspacePath)
	if err != nil {
		return fmt.Errorf("capture repo-intel working tree: %w", err)
	}
	if workingTree != "git-tree:"+strings.TrimSpace(headTree) {
		return fmt.Errorf("repo-intel working tree does not match requested commit %s", requestedHash)
	}
	return nil
}

func maxInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
