package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NewHTTPBasicPublisher wraps a Publisher with ephemeral Git HTTPS basic
// authentication. Credentials are passed through process environment variables,
// never command arguments or repository URLs.
func NewHTTPBasicPublisher(inner Publisher, username, password string) Publisher {
	return &httpBasicPublisher{
		inner:    inner,
		username: username,
		password: password,
	}
}

type httpBasicPublisher struct {
	inner    Publisher
	username string
	password string
}

func (p *httpBasicPublisher) Publish(ctx context.Context, req PublishRequest) error {
	if p.inner == nil {
		return fmt.Errorf("branch publisher is required")
	}
	if strings.TrimSpace(p.username) == "" {
		return fmt.Errorf("git HTTPS username is required")
	}
	if p.password == "" {
		return fmt.Errorf("git HTTPS password is required")
	}

	dir, err := os.MkdirTemp("", "dev-plane-git-askpass-*")
	if err != nil {
		return fmt.Errorf("create git askpass dir: %w", err)
	}
	defer os.RemoveAll(dir)

	script := filepath.Join(dir, "askpass.sh")
	scriptBody := "#!/bin/sh\ncase \"$1\" in\n*Username*) printf '%s\\n' \"$DEV_PLANE_GIT_USERNAME\" ;;\n*) printf '%s\\n' \"$DEV_PLANE_GIT_PASSWORD\" ;;\nesac\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o700); err != nil {
		return fmt.Errorf("write git askpass helper: %w", err)
	}

	env := make(map[string]string, len(req.Env)+4)
	for key, value := range req.Env {
		env[key] = value
	}
	env["GIT_ASKPASS"] = script
	env["GIT_TERMINAL_PROMPT"] = "0"
	env["DEV_PLANE_GIT_USERNAME"] = p.username
	env["DEV_PLANE_GIT_PASSWORD"] = p.password
	req.Env = env

	return p.inner.Publish(ctx, req)
}

func publishRemote(remote string) string {
	if trimmed := strings.TrimSpace(remote); trimmed != "" {
		return trimmed
	}
	return "origin"
}
