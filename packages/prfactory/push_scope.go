package prfactory

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// validGitHubPushURL accepts only the exact selected GitHub repository.
// This prevents a workspace-controlled origin from receiving a scoped token.
func validGitHubPushURL(raw, owner, repo string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") ||
		parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.RawPath != "" {
		return false
	}
	want := "/" + owner + "/" + repo
	return strings.EqualFold(parsed.Path, want) || strings.EqualFold(parsed.Path, want+".git")
}

// sanitizedGitEnvironment strips user/global credentials and rewrite-injection
// environment variables. The checked origin and final push see the same env.
func sanitizedGitEnvironment() []string {
	ignored := []string{"GITHUB_TOKEN=", "GH_TOKEN=", "GIT_ASKPASS=", "SSH_ASKPASS=",
		"GIT_CONFIG_COUNT=", "GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_", "GIT_SSH_COMMAND=", "GIT_SSH=",
		"GIT_CONFIG_GLOBAL=", "GIT_CONFIG_NOSYSTEM=", "GIT_ALLOW_PROTOCOL="}
	result := make([]string, 0, len(os.Environ())+4)
	for _, item := range os.Environ() {
		block := false
		for _, prefix := range ignored {
			if strings.HasPrefix(item, prefix) {
				block = true
				break
			}
		}
		if !block {
			result = append(result, item)
		}
	}
	return append(result, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_ALLOW_PROTOCOL=https", "GIT_TERMINAL_PROMPT=0")
}

func verifyGitPushRemote(ctx context.Context, workspacePath, branch, owner, repo string, env []string) error {
	if strings.EqualFold(branch, "main") || strings.EqualFold(branch, "master") {
		return errors.New("direct push to protected branch is forbidden")
	}
	refcheck := exec.CommandContext(ctx, "git", "-C", workspacePath, "check-ref-format", "--branch", branch)
	refcheck.Env = env
	if err := refcheck.Run(); err != nil {
		return errors.New("invalid agent feature branch")
	}
	// Git resolves url.*.insteadOf and pushInsteadOf in remote get-url --push.
	remote := exec.CommandContext(ctx, "git", "-C", workspacePath, "remote", "get-url", "--push", "--all", "origin")
	remote.Env = env
	out, err := remote.Output()
	if err != nil {
		return errors.New("cannot resolve git origin push URL")
	}
	urls := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(urls) != 1 || !validGitHubPushURL(strings.TrimSpace(urls[0]), owner, repo) {
		return errors.New("git origin push remote does not match authorized GitHub repository")
	}
	return nil
}
