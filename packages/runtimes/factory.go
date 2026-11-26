package runtimes

import (
	"fmt"
	"strings"
)

// NewProvider constructs a workspace runtime Provider by name.
// Supported names are "local", "docker", "remote", and "nulang-cloud".
// For the "remote" provider, runnerURL must be a non-empty runner endpoint.
func NewProvider(name, baseDir, runnerURL, runnerToken string) (Provider, string, error) {
	switch strings.ToLower(name) {
	case "local":
		return NewLocalProvider(baseDir), "local", nil
	case "docker":
		provider, err := NewDockerProvider(baseDir)
		if err != nil {
			return nil, "", err
		}
		return provider, "docker", nil
	case "nulang-cloud", "nulangcloud":
		provider, err := NewNulangCloudProviderFromEnv()
		if err != nil {
			return nil, "", err
		}
		return provider, "nulang-cloud", nil
	case "remote":
		if runnerURL == "" {
			return nil, "", fmt.Errorf("remote runtime requires RUNNER_URL")
		}
		return NewRemoteProvider(runnerURL, runnerToken), "remote", nil
	default:
		return nil, "", fmt.Errorf("unsupported workspace runtime %q", name)
	}
}
