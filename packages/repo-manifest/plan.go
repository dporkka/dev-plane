package repomanifest

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

var validationKindOrder = []string{"lint", "typecheck", "test", "build"}

// PlannedCheck is one concrete validation command selected for a change set.
type PlannedCheck struct {
	Component string  `json:"component,omitempty"`
	Kind      string  `json:"kind"`
	Command   Command `json:"command"`
}

// CheckPlan describes the minimum manifest-declared validation required for a
// set of changed repository files.
type CheckPlan struct {
	ChangedFiles       []string       `json:"changed_files"`
	ChangedComponents  []string       `json:"changed_components"`
	AffectedComponents []string       `json:"affected_components"`
	Checks             []PlannedCheck `json:"checks"`
}

// PlanChecks maps changed files to directly changed components, expands reverse
// dependents, and returns deterministic validation commands.
func (m *Manifest) PlanChecks(changedFiles []string) (CheckPlan, error) {
	if err := m.Validate(); err != nil {
		return CheckPlan{}, err
	}
	normalized, err := normalizeChangedFiles(changedFiles)
	if err != nil {
		return CheckPlan{}, err
	}
	plan := CheckPlan{ChangedFiles: normalized}
	if len(normalized) == 0 {
		return plan, nil
	}

	changed := map[string]bool{}
	names := sortedComponentNames(m.Components)
	for _, name := range names {
		if componentMatches(m.Components[name], normalized) {
			changed[name] = true
		}
	}
	plan.ChangedComponents = sortedSet(changed)

	if len(changed) == 0 {
		plan.Checks = m.fallbackChecks()
		return plan, nil
	}

	affected := map[string]bool{}
	for name := range changed {
		affected[name] = true
	}
	for progressed := true; progressed; {
		progressed = false
		for _, name := range names {
			if affected[name] {
				continue
			}
			for _, dependency := range m.Components[name].DependsOn {
				if affected[dependency] {
					affected[name] = true
					progressed = true
					break
				}
			}
		}
	}

	plan.AffectedComponents = sortedSet(affected)
	plan.Checks = m.componentChecks(plan.AffectedComponents)
	return plan, nil
}

func (m *Manifest) fallbackChecks() []PlannedCheck {
	kinds := m.Validation.FallbackChecks
	if len(kinds) == 0 {
		for _, kind := range validationKindOrder {
			if commandForKind(m.Commands, kind) != nil {
				kinds = append(kinds, kind)
			}
		}
	}

	checks := make([]PlannedCheck, 0, len(kinds))
	seen := map[string]bool{}
	for _, kind := range kinds {
		command := commandForKind(m.Commands, kind)
		if command == nil {
			continue
		}
		key := kind + "\x00" + command.Run
		if seen[key] {
			continue
		}
		seen[key] = true
		checks = append(checks, PlannedCheck{Kind: kind, Command: *command})
	}
	return orderChecks(checks)
}

func (m *Manifest) componentChecks(affected []string) []PlannedCheck {
	var checks []PlannedCheck
	seen := map[string]bool{}
	for _, name := range affected {
		component := m.Components[name]
		added := false
		for _, kind := range validationKindOrder {
			command := commandForKind(component.Checks, kind)
			if command == nil {
				continue
			}
			added = true
			key := kind + "\x00" + command.Run
			if seen[key] {
				continue
			}
			seen[key] = true
			checks = append(checks, PlannedCheck{Component: name, Kind: kind, Command: *command})
		}

		// A component without scoped checks must not silently escape validation.
		if !added {
			for _, check := range m.fallbackChecks() {
				key := check.Kind + "\x00" + check.Command.Run
				if seen[key] {
					continue
				}
				seen[key] = true
				checks = append(checks, check)
			}
		}
	}
	return orderChecks(checks)
}

func orderChecks(checks []PlannedCheck) []PlannedCheck {
	rank := map[string]int{}
	for i, kind := range validationKindOrder {
		rank[kind] = i
	}
	sort.SliceStable(checks, func(i, j int) bool {
		ri, iKnown := rank[checks[i].Kind]
		rj, jKnown := rank[checks[j].Kind]
		if iKnown && jKnown && ri != rj {
			return ri < rj
		}
		if checks[i].Component != checks[j].Component {
			return checks[i].Component < checks[j].Component
		}
		return checks[i].Kind < checks[j].Kind
	})
	return checks
}

func commandForKind(commands Commands, kind string) *Command {
	switch kind {
	case "lint":
		return commands.Lint
	case "typecheck":
		return commands.Typecheck
	case "test":
		return commands.Test
	case "build":
		return commands.Build
	default:
		return nil
	}
}

func isValidationKind(kind string) bool {
	return commandForKind(Commands{
		Lint:      &Command{},
		Typecheck: &Command{},
		Test:      &Command{},
		Build:     &Command{},
	}, kind) != nil
}

func normalizeChangedFiles(files []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(files))
	for _, file := range files {
		clean := strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(file, "\\", "/")), "./")
		clean = path.Clean(clean)
		if clean == "." || clean == "" {
			continue
		}
		if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("changed file %q is outside repository", file)
		}
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	sort.Strings(out)
	return out, nil
}

func componentMatches(component Component, files []string) bool {
	for _, pattern := range component.Paths {
		for _, file := range files {
			if matchPathPattern(pattern, file) {
				return true
			}
		}
	}
	return false
}

func matchPathPattern(pattern, file string) bool {
	pattern = strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./")
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		return file == prefix || strings.HasPrefix(file, prefix+"/")
	}
	ok, err := path.Match(pattern, file)
	return err == nil && ok
}

func sortedComponentNames(components map[string]Component) []string {
	out := make([]string, 0, len(components))
	for name := range components {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		if set[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
