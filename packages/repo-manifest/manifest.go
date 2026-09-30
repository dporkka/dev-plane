// Package repomanifest defines the stable, repository-owned contract Dev Plane
// uses before falling back to stack-specific auto-detection.
package repomanifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// Filename is the canonical repository manifest name.
	Filename = "dev-plane.json"

	// CurrentSchemaVersion is the only schema version supported by this package.
	CurrentSchemaVersion = 1

	// MaxTimeoutSeconds bounds repository-provided command timeouts.
	MaxTimeoutSeconds = 3600
)

// Manifest declares repository lifecycle commands and affected-validation
// metadata without coupling the repository to Dev Plane implementation details.
type Manifest struct {
	SchemaVersion int                  `json:"schema_version"`
	Name          string               `json:"name,omitempty"`
	Commands      Commands             `json:"commands,omitempty"`
	Components    map[string]Component `json:"components,omitempty"`
	Validation    Validation           `json:"validation,omitempty"`
}

// Commands contains lifecycle or validation commands Dev Plane can execute.
type Commands struct {
	Test      *Command `json:"test,omitempty"`
	Lint      *Command `json:"lint,omitempty"`
	Typecheck *Command `json:"typecheck,omitempty"`
	Build     *Command `json:"build,omitempty"`
	Dev       *Command `json:"dev,omitempty"`
}

// Command describes a repository-owned command.
type Command struct {
	Run            string `json:"run"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// Component maps repository paths to the checks that validate that component.
// DependsOn points from a consumer to the components it depends on.
type Component struct {
	Paths     []string `json:"paths,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Checks    Commands `json:"checks,omitempty"`
}

// Validation defines conservative behavior when changed files do not map to a
// declared component.
type Validation struct {
	FallbackChecks []string `json:"fallback_checks,omitempty"`
}

// Load reads and validates the canonical manifest from root.
func Load(root string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, Filename))
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes and validates a manifest.
func Parse(data []byte) (*Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Filename, err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// Validate checks schema compatibility, command invariants, path rules, and the
// component dependency graph.
func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	if m.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d; want %d", m.SchemaVersion, CurrentSchemaVersion)
	}
	if err := validateCommands("commands", m.Commands, true); err != nil {
		return err
	}

	for name, component := range m.Components {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("components must not contain a blank name")
		}
		for _, pattern := range component.Paths {
			if err := validatePathPattern(pattern); err != nil {
				return fmt.Errorf("components.%s.paths: %w", name, err)
			}
		}
		if err := validateCommands("components."+name+".checks", component.Checks, false); err != nil {
			return err
		}
		if component.Checks.Dev != nil {
			return fmt.Errorf("components.%s.checks.dev is not a validation check", name)
		}
		for _, dependency := range component.DependsOn {
			if _, ok := m.Components[dependency]; !ok {
				return fmt.Errorf("components.%s.depends_on references unknown component %q", name, dependency)
			}
		}
	}
	if err := validateComponentGraph(m.Components); err != nil {
		return err
	}
	if len(m.Components) > 0 && !hasValidationCommand(m.Commands) {
		return fmt.Errorf("components require at least one top-level validation command for conservative fallback")
	}

	for _, kind := range m.Validation.FallbackChecks {
		if !isValidationKind(kind) {
			return fmt.Errorf("validation.fallback_checks contains unsupported check %q", kind)
		}
		if commandForKind(m.Commands, kind) == nil {
			return fmt.Errorf("validation.fallback_checks references undeclared commands.%s", kind)
		}
	}
	return nil
}

func validateCommands(prefix string, commands Commands, allowDev bool) error {
	for _, item := range []struct {
		name string
		cmd  *Command
	}{
		{"test", commands.Test},
		{"lint", commands.Lint},
		{"typecheck", commands.Typecheck},
		{"build", commands.Build},
		{"dev", commands.Dev},
	} {
		if item.cmd == nil {
			continue
		}
		if item.name == "dev" && !allowDev {
			continue
		}
		if strings.TrimSpace(item.cmd.Run) == "" {
			return fmt.Errorf("%s.%s.run must not be blank", prefix, item.name)
		}
		if item.cmd.TimeoutSeconds < 0 || item.cmd.TimeoutSeconds > MaxTimeoutSeconds {
			return fmt.Errorf("%s.%s.timeout_seconds must be between 0 and %d", prefix, item.name, MaxTimeoutSeconds)
		}
	}
	return nil
}

func validatePathPattern(pattern string) error {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if pattern == "" {
		return fmt.Errorf("path pattern must not be blank")
	}
	if strings.HasPrefix(pattern, "/") || pattern == ".." || strings.HasPrefix(pattern, "../") || strings.Contains(pattern, "/../") {
		return fmt.Errorf("path pattern %q must stay within the repository", pattern)
	}
	if strings.Contains(strings.TrimSuffix(pattern, "/**"), "**") {
		return fmt.Errorf("path pattern %q may only use ** as a trailing /**", pattern)
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		if strings.ContainsAny(prefix, "*?[") {
			return fmt.Errorf("path pattern %q must use a literal prefix before /**", pattern)
		}
		return nil
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid path pattern %q: %w", pattern, err)
	}
	return nil
}

func hasValidationCommand(commands Commands) bool {
	return commands.Lint != nil || commands.Typecheck != nil || commands.Test != nil || commands.Build != nil
}

func validateComponentGraph(components map[string]Component) error {
	state := make(map[string]uint8, len(components))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("component dependency cycle includes %q", name)
		case 2:
			return nil
		}
		state[name] = 1
		for _, dependency := range components[name].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for name := range components {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}
