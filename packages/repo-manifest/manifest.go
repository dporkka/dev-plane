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

// Manifest declares repository lifecycle commands without coupling the
// repository to Dev Plane implementation details.
type Manifest struct {
	SchemaVersion int      `json:"schema_version"`
	Name          string   `json:"name,omitempty"`
	Commands      Commands `json:"commands,omitempty"`
}

// Commands contains the lifecycle commands Dev Plane can execute.
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

// Validate checks schema compatibility and command invariants.
func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("manifest is nil")
	}
	if m.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d; want %d", m.SchemaVersion, CurrentSchemaVersion)
	}

	for _, item := range []struct {
		name string
		cmd  *Command
	}{
		{"test", m.Commands.Test},
		{"lint", m.Commands.Lint},
		{"typecheck", m.Commands.Typecheck},
		{"build", m.Commands.Build},
		{"dev", m.Commands.Dev},
	} {
		if item.cmd == nil {
			continue
		}
		if strings.TrimSpace(item.cmd.Run) == "" {
			return fmt.Errorf("commands.%s.run must not be blank", item.name)
		}
		if item.cmd.TimeoutSeconds < 0 || item.cmd.TimeoutSeconds > MaxTimeoutSeconds {
			return fmt.Errorf("commands.%s.timeout_seconds must be between 0 and %d", item.name, MaxTimeoutSeconds)
		}
	}

	return nil
}
