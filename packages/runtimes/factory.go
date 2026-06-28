package runtimes

import (
	"fmt"
	"strings"
)

// NewProvider constructs a workspace runtime Provider by name.
// Supported names are "local" and "docker".
func NewProvider(name, baseDir string) (Provider, string, error) {
	switch strings.ToLower(name) {
	case "local":
		return NewLocalProvider(baseDir), "local", nil
	case "docker":
		provider, err := NewDockerProvider(baseDir)
		if err != nil {
			return nil, "", err
		}
		return provider, "docker", nil
	default:
		return nil, "", fmt.Errorf("unsupported workspace runtime %q", name)
	}
}
