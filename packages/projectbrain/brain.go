package projectbrain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	ContextPackageVersion = 1
	DefaultMaxFacts       = 24
	MaxFactsLimit         = 200
)

type Scope string

const (
	ScopeRevision   Scope = "revision"
	ScopeRepository Scope = "repository"
)

type Provenance struct {
	Source     string    `json:"source"`
	Reference  string    `json:"reference,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitempty"`
	Confidence float64   `json:"confidence"`
}

type Fact struct {
	Subject    string       `json:"subject"`
	Relation   string       `json:"relation"`
	Object     string       `json:"object"`
	Repository string       `json:"repository"`
	Revision   string       `json:"revision,omitempty"`
	Scope      Scope        `json:"scope"`
	Provenance []Provenance `json:"provenance"`
}

func (f Fact) Validate() error {
	if strings.TrimSpace(f.Subject) == "" {
		return errors.New("fact subject is required")
	}
	if strings.TrimSpace(f.Relation) == "" {
		return errors.New("fact relation is required")
	}
	if strings.TrimSpace(f.Object) == "" {
		return errors.New("fact object is required")
	}
	if strings.TrimSpace(f.Repository) == "" {
		return errors.New("fact repository is required")
	}
	switch f.Scope {
	case ScopeRevision:
		if strings.TrimSpace(f.Revision) == "" {
			return errors.New("revision-scoped fact requires revision")
		}
	case ScopeRepository:
	case "":
		return errors.New("fact scope is required")
	default:
		return fmt.Errorf("unsupported fact scope %q", f.Scope)
	}
	if len(f.Provenance) == 0 {
		return errors.New("fact requires provenance")
	}
	for i, p := range f.Provenance {
		if strings.TrimSpace(p.Source) == "" {
			return fmt.Errorf("fact provenance %d source is required", i)
		}
		if p.Confidence < 0 || p.Confidence > 1 {
			return fmt.Errorf("fact provenance %d confidence must be between 0 and 1", i)
		}
	}
	return nil
}

type Query struct {
	Repository         string   `json:"repository"`
	Revision           string   `json:"revision"`
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	ChangedPaths       []string `json:"changed_paths,omitempty"`
	Terms              []string `json:"terms,omitempty"`
}

type Source interface {
	Name() string
	Facts(context.Context, Query) ([]Fact, error)
}

type CompileRequest struct {
	Repository         string   `json:"repository"`
	Revision           string   `json:"revision"`
	Objective          string   `json:"objective"`
	AcceptanceCriteria []string `json:"acceptance_criteria,omitempty"`
	ChangedPaths       []string `json:"changed_paths,omitempty"`
	MaxFacts           int      `json:"max_facts,omitempty"`
}

type Warning struct {
	Source string `json:"source"`
	Error  string `json:"error"`
}

type ContextPackage struct {
	Version            int       `json:"version"`
	Repository         string    `json:"repository"`
	Revision           string    `json:"revision"`
	Objective          string    `json:"objective"`
	AcceptanceCriteria []string  `json:"acceptance_criteria,omitempty"`
	ChangedPaths       []string  `json:"changed_paths,omitempty"`
	Facts              []Fact    `json:"facts"`
	Sources            []string  `json:"sources,omitempty"`
	Warnings           []Warning `json:"warnings,omitempty"`
	Digest             string    `json:"digest"`
}

type Compiler struct {
	sources []Source
}

func NewCompiler(sources ...Source) *Compiler {
	return &Compiler{sources: append([]Source(nil), sources...)}
}

func (c *Compiler) Compile(ctx context.Context, req CompileRequest) (ContextPackage, error) {
	req.Repository = strings.TrimSpace(req.Repository)
	req.Revision = strings.TrimSpace(req.Revision)
	req.Objective = strings.TrimSpace(req.Objective)
	if req.Repository == "" {
		return ContextPackage{}, errors.New("context repository is required")
	}
	if req.Revision == "" {
		return ContextPackage{}, errors.New("context revision is required")
	}
	if req.Objective == "" {
		return ContextPackage{}, errors.New("context objective is required")
	}
	if req.MaxFacts < 0 {
		return ContextPackage{}, errors.New("max_facts cannot be negative")
	}
	maxFacts := req.MaxFacts
	if maxFacts == 0 {
		maxFacts = DefaultMaxFacts
	}
	if maxFacts > MaxFactsLimit {
		maxFacts = MaxFactsLimit
	}

	terms := queryTerms(req)
	query := Query{
		Repository:         req.Repository,
		Revision:           req.Revision,
		Objective:          req.Objective,
		AcceptanceCriteria: append([]string(nil), req.AcceptanceCriteria...),
		ChangedPaths:       append([]string(nil), req.ChangedPaths...),
		Terms:              terms,
	}

	byIdentity := make(map[string]Fact)
	warnings := make([]Warning, 0)
	sourceSet := make(map[string]struct{})

	for _, source := range c.sources {
		if source == nil {
			warnings = append(warnings, Warning{Source: "<nil>", Error: "source is nil"})
			continue
		}
		sourceName := strings.TrimSpace(source.Name())
		if sourceName == "" {
			sourceName = "<unnamed>"
		}
		facts, err := source.Facts(ctx, query)
		if err != nil {
			warnings = append(warnings, Warning{Source: sourceName, Error: err.Error()})
			continue
		}
		sourceSet[sourceName] = struct{}{}
		for _, raw := range facts {
			fact := normalizeFact(raw, sourceName)
			if err := fact.Validate(); err != nil {
				warnings = append(warnings, Warning{Source: sourceName, Error: err.Error()})
				continue
			}
			if fact.Repository != req.Repository {
				continue
			}
			if fact.Scope == ScopeRevision && fact.Revision != req.Revision {
				continue
			}

			key := factIdentity(fact)
			if current, exists := byIdentity[key]; exists {
				current.Provenance = mergeProvenance(current.Provenance, fact.Provenance)
				byIdentity[key] = current
				continue
			}
			fact.Provenance = mergeProvenance(nil, fact.Provenance)
			byIdentity[key] = fact
		}
	}

	facts := make([]Fact, 0, len(byIdentity))
	for _, fact := range byIdentity {
		facts = append(facts, fact)
	}
	sort.Slice(facts, func(i, j int) bool {
		iMatches := factMatchCount(facts[i], terms)
		jMatches := factMatchCount(facts[j], terms)
		if iMatches != jMatches {
			return iMatches > jMatches
		}
		iSpecific := scopeSpecificity(facts[i].Scope)
		jSpecific := scopeSpecificity(facts[j].Scope)
		if iSpecific != jSpecific {
			return iSpecific > jSpecific
		}
		iConfidence := maxConfidence(facts[i].Provenance)
		jConfidence := maxConfidence(facts[j].Provenance)
		if iConfidence != jConfidence {
			return iConfidence > jConfidence
		}
		return factSortKey(facts[i]) < factSortKey(facts[j])
	})
	if len(facts) > maxFacts {
		facts = facts[:maxFacts]
	}

	sources := make([]string, 0, len(sourceSet))
	for source := range sourceSet {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].Source != warnings[j].Source {
			return warnings[i].Source < warnings[j].Source
		}
		return warnings[i].Error < warnings[j].Error
	})

	pkg := ContextPackage{
		Version:            ContextPackageVersion,
		Repository:         req.Repository,
		Revision:           req.Revision,
		Objective:          req.Objective,
		AcceptanceCriteria: append([]string(nil), req.AcceptanceCriteria...),
		ChangedPaths:       append([]string(nil), req.ChangedPaths...),
		Facts:              facts,
		Sources:            sources,
		Warnings:           warnings,
	}
	digest, err := packageDigest(pkg)
	if err != nil {
		return ContextPackage{}, err
	}
	pkg.Digest = digest
	return pkg, nil
}

func normalizeFact(f Fact, sourceName string) Fact {
	f.Subject = strings.TrimSpace(f.Subject)
	f.Relation = strings.TrimSpace(f.Relation)
	f.Object = strings.TrimSpace(f.Object)
	f.Repository = strings.TrimSpace(f.Repository)
	f.Revision = strings.TrimSpace(f.Revision)
	for i := range f.Provenance {
		f.Provenance[i].Source = strings.TrimSpace(f.Provenance[i].Source)
		if f.Provenance[i].Source == "" {
			f.Provenance[i].Source = sourceName
		}
		f.Provenance[i].Reference = strings.TrimSpace(f.Provenance[i].Reference)
	}
	return f
}

func factIdentity(f Fact) string {
	return strings.Join([]string{
		string(f.Scope),
		f.Repository,
		f.Revision,
		f.Subject,
		f.Relation,
		f.Object,
	}, "\x00")
}

func mergeProvenance(existing, incoming []Provenance) []Provenance {
	byKey := make(map[string]Provenance, len(existing)+len(incoming))
	for _, p := range append(append([]Provenance(nil), existing...), incoming...) {
		key := p.Source + "\x00" + p.Reference
		if current, ok := byKey[key]; ok {
			if p.Confidence > current.Confidence {
				current.Confidence = p.Confidence
			}
			if p.ObservedAt.After(current.ObservedAt) {
				current.ObservedAt = p.ObservedAt
			}
			byKey[key] = current
			continue
		}
		byKey[key] = p
	}
	result := make([]Provenance, 0, len(byKey))
	for _, p := range byKey {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Reference < result[j].Reference
	})
	return result
}

func queryTerms(req CompileRequest) []string {
	seen := make(map[string]struct{})
	terms := make([]string, 0)
	add := func(value string) {
		for _, token := range tokenize(value) {
			if _, exists := seen[token]; exists {
				continue
			}
			seen[token] = struct{}{}
			terms = append(terms, token)
		}
	}
	add(req.Objective)
	for _, criterion := range req.AcceptanceCriteria {
		add(criterion)
	}
	for _, changed := range req.ChangedPaths {
		add(changed)
	}
	sort.Strings(terms)
	return terms
}

func tokenize(value string) []string {
	parts := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	result := parts[:0]
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		result = append(result, part)
	}
	return result
}

func factMatchCount(f Fact, terms []string) int {
	text := strings.ToLower(strings.Join([]string{f.Subject, f.Relation, f.Object}, " "))
	for _, p := range f.Provenance {
		text += " " + strings.ToLower(p.Reference)
	}
	matches := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			matches++
		}
	}
	return matches
}

func scopeSpecificity(scope Scope) int {
	if scope == ScopeRevision {
		return 1
	}
	return 0
}

func maxConfidence(provenance []Provenance) float64 {
	var max float64
	for _, p := range provenance {
		if p.Confidence > max {
			max = p.Confidence
		}
	}
	return max
}

func factSortKey(f Fact) string {
	return strings.Join([]string{f.Subject, f.Relation, f.Object, string(f.Scope), f.Revision}, "\x00")
}

type digestProvenance struct {
	Source     string  `json:"source"`
	Reference  string  `json:"reference,omitempty"`
	Confidence float64 `json:"confidence"`
}

type digestFact struct {
	Subject    string             `json:"subject"`
	Relation   string             `json:"relation"`
	Object     string             `json:"object"`
	Repository string             `json:"repository"`
	Revision   string             `json:"revision,omitempty"`
	Scope      Scope              `json:"scope"`
	Provenance []digestProvenance `json:"provenance"`
}

type digestPackage struct {
	Version            int          `json:"version"`
	Repository         string       `json:"repository"`
	Revision           string       `json:"revision"`
	Objective          string       `json:"objective"`
	AcceptanceCriteria []string     `json:"acceptance_criteria,omitempty"`
	ChangedPaths       []string     `json:"changed_paths,omitempty"`
	Facts              []digestFact `json:"facts"`
}

func packageDigest(pkg ContextPackage) (string, error) {
	canonical := digestPackage{
		Version:            pkg.Version,
		Repository:         pkg.Repository,
		Revision:           pkg.Revision,
		Objective:          pkg.Objective,
		AcceptanceCriteria: append([]string(nil), pkg.AcceptanceCriteria...),
		ChangedPaths:       append([]string(nil), pkg.ChangedPaths...),
		Facts:              make([]digestFact, 0, len(pkg.Facts)),
	}
	for _, fact := range pkg.Facts {
		entry := digestFact{
			Subject:    fact.Subject,
			Relation:   fact.Relation,
			Object:     fact.Object,
			Repository: fact.Repository,
			Revision:   fact.Revision,
			Scope:      fact.Scope,
			Provenance: make([]digestProvenance, 0, len(fact.Provenance)),
		}
		for _, p := range fact.Provenance {
			entry.Provenance = append(entry.Provenance, digestProvenance{
				Source: p.Source, Reference: p.Reference, Confidence: p.Confidence,
			})
		}
		canonical.Facts = append(canonical.Facts, entry)
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal context package digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
