package projectbrain

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const defaultGitNexusMaxResults = 50

type GitNexusSnapshot struct {
	LastCommit string    `json:"last_commit"`
	IndexedAt  time.Time `json:"indexed_at,omitempty"`
}

type GitNexusNode struct {
	UID        string  `json:"uid,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	Name       string  `json:"name"`
	FilePath   string  `json:"file_path,omitempty"`
	StartLine  int     `json:"start_line,omitempty"`
	EndLine    int     `json:"end_line,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type GitNexusRelation struct {
	From       string  `json:"from"`
	Type       string  `json:"type"`
	To         string  `json:"to"`
	Reference  string  `json:"reference,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type GitNexusProcessStep struct {
	Position  int    `json:"position"`
	Symbol    string `json:"symbol"`
	FilePath  string `json:"file_path,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
}

type GitNexusProcess struct {
	Name      string                `json:"name"`
	Reference string                `json:"reference,omitempty"`
	Steps     []GitNexusProcessStep `json:"steps"`
}

type GitNexusResult struct {
	Nodes     []GitNexusNode     `json:"nodes,omitempty"`
	Relations []GitNexusRelation `json:"relations,omitempty"`
	Processes []GitNexusProcess  `json:"processes,omitempty"`
}

type GitNexusClient interface {
	Snapshot(ctx context.Context, repository string) (GitNexusSnapshot, error)
	Query(ctx context.Context, repository string, terms []string, limit int) (GitNexusResult, error)
}

type GitNexusSourceOptions struct {
	MaxResults int
}

type GitNexusSource struct {
	client     GitNexusClient
	maxResults int
}

func NewGitNexusSource(client GitNexusClient, options GitNexusSourceOptions) *GitNexusSource {
	maxResults := options.MaxResults
	if maxResults <= 0 {
		maxResults = defaultGitNexusMaxResults
	}
	return &GitNexusSource{client: client, maxResults: maxResults}
}

func (s *GitNexusSource) Name() string { return "gitnexus" }

func (s *GitNexusSource) Facts(ctx context.Context, query Query) ([]Fact, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("gitnexus client is required")
	}
	repository := strings.TrimSpace(query.Repository)
	if repository == "" {
		return nil, errors.New("gitnexus repository is required")
	}
	commit, err := gitCommitRevision(query.Revision)
	if err != nil {
		return nil, err
	}

	snapshot, err := s.client.Snapshot(ctx, repository)
	if err != nil {
		return nil, fmt.Errorf("gitnexus snapshot: %w", err)
	}
	indexedCommit := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(snapshot.LastCommit), "git-commit:"))
	if indexedCommit == "" {
		return nil, errors.New("gitnexus snapshot last commit is empty")
	}
	if indexedCommit != commit {
		return nil, fmt.Errorf("gitnexus index is stale: indexed=%s requested=%s", indexedCommit, commit)
	}

	result, err := s.client.Query(ctx, repository, append([]string(nil), query.Terms...), s.maxResults)
	if err != nil {
		return nil, fmt.Errorf("gitnexus query: %w", err)
	}

	revision := "git-commit:" + commit
	observedAt := snapshot.IndexedAt
	facts := make([]Fact, 0, len(result.Nodes)+len(result.Relations)+len(result.Processes))
	for _, node := range result.Nodes {
		name := strings.TrimSpace(node.Name)
		if name == "" {
			continue
		}
		location := locationString(node.FilePath, node.StartLine)
		if location == "" {
			continue
		}
		reference := strings.TrimSpace(node.UID)
		if reference == "" {
			reference = "symbol:" + name
		}
		facts = append(facts, Fact{
			Subject:    "symbol:" + name,
			Relation:   "defined_in",
			Object:     location,
			Repository: repository,
			Revision:   revision,
			Scope:      ScopeRevision,
			Provenance: []Provenance{{Source: s.Name(), Reference: reference, ObservedAt: observedAt, Confidence: normalizedConfidence(node.Confidence, 1)}},
		})
	}
	for _, relation := range result.Relations {
		from := strings.TrimSpace(relation.From)
		to := strings.TrimSpace(relation.To)
		relType := strings.ToLower(strings.TrimSpace(relation.Type))
		if from == "" || to == "" || relType == "" {
			continue
		}
		reference := strings.TrimSpace(relation.Reference)
		if reference == "" {
			reference = from + "->" + relation.Type + "->" + to
		}
		facts = append(facts, Fact{
			Subject:    "symbol:" + from,
			Relation:   relType,
			Object:     "symbol:" + to,
			Repository: repository,
			Revision:   revision,
			Scope:      ScopeRevision,
			Provenance: []Provenance{{Source: s.Name(), Reference: reference, ObservedAt: observedAt, Confidence: normalizedConfidence(relation.Confidence, 0.8)}},
		})
	}
	for _, process := range result.Processes {
		processName := strings.TrimSpace(process.Name)
		if processName == "" {
			continue
		}
		for _, step := range process.Steps {
			symbol := strings.TrimSpace(step.Symbol)
			if symbol == "" {
				continue
			}
			position := step.Position
			if position <= 0 {
				position = 1
			}
			object := strconv.Itoa(position) + ":symbol:" + symbol
			if location := locationString(step.FilePath, step.StartLine); location != "" {
				object += "@" + location
			}
			reference := strings.TrimSpace(process.Reference)
			if reference == "" {
				reference = "process:" + processName
			}
			facts = append(facts, Fact{
				Subject:    "process:" + processName,
				Relation:   "step",
				Object:     object,
				Repository: repository,
				Revision:   revision,
				Scope:      ScopeRevision,
				Provenance: []Provenance{{Source: s.Name(), Reference: reference, ObservedAt: observedAt, Confidence: 1}},
			})
		}
	}
	return facts, nil
}

func gitCommitRevision(revision string) (string, error) {
	revision = strings.TrimSpace(revision)
	if !strings.HasPrefix(revision, "git-commit:") {
		return "", fmt.Errorf("gitnexus source requires git-commit revision, got %q", revision)
	}
	commit := strings.TrimSpace(strings.TrimPrefix(revision, "git-commit:"))
	if commit == "" {
		return "", errors.New("gitnexus git-commit revision is empty")
	}
	return commit, nil
}

func locationString(filePath string, line int) string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return ""
	}
	if line > 0 {
		return filePath + ":" + strconv.Itoa(line)
	}
	return filePath
}

func normalizedConfidence(value, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	if value > 1 {
		return 1
	}
	return value
}
