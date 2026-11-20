package prfactory

import (
	"context"
	"errors"
	"fmt"
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

// sanitizedGitEnvironment strips all inherited Git settings, including
// repository selectors (GIT_DIR, GIT_WORK_TREE, GIT_INDEX_FILE) and transport
// overrides. Only the explicitly pinned Git settings below are reinstated.
// The checked origin and final push must use this same controlled environment.
func sanitizedGitEnvironment() []string {
	ignored := []string{"GITHUB_TOKEN=", "GH_TOKEN=", "SSH_ASKPASS=", "GIT_"}
	result := make([]string, 0, len(os.Environ())+4)
	for _, item := range os.Environ() {
		block := false
		for _, prefix := range ignored {
			if strings.HasPrefix(item, prefix) {
				block = true
				break
			}
		}
		key := strings.ToUpper(strings.SplitN(item, "=", 2)[0])
		switch key {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY",
			"CURL_CA_BUNDLE", "SSL_CERT_FILE", "SSL_CERT_DIR",
			"REQUESTS_CA_BUNDLE":
			block = true
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

// trustedGitTransportArgs pins the HTTP/TLS policy for the only authorized
// GitHub repository. Git matches URL-specific http.* settings by longest URL,
// so global -c http.proxy= / http.sslVerify=true ALONE cannot override
// malicious workspace-local http.https://github.com/owner/repo.* values.
// Note: validation of arbitrary local Git configuration/rewrite and
// race-free transport isolation still requires a separate release gate.
func trustedGitTransportArgs(owner, repo string) ([]string, error) {
	valid := func(value string, isOwner bool) bool {
		if len(value) == 0 || len(value) > 100 || value == "." || value == ".." {
			return false
		}
		for _, char := range value {
			if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
				char >= '0' && char <= '9' || char == '-' {
				continue
			}
			if !isOwner && (char == '.' || char == '_') {
				continue
			}
			return false
		}
		return true
	}
	if !valid(owner, true) || !valid(repo, false) {
		return nil, fmt.Errorf("invalid GitHub repository segments")
	}
	// The actual push remote might contain '.git' or omit it. Protect both.
	opts := []string{
		"-c", "http.proxy=",
		"-c", "remote.origin.proxy=",
		"-c", "http.sslVerify=true",
		"-c", "http.sslCAInfo=",
		"-c", "http.sslCAPath=",
		"-c", "http.extraHeader=",
		"-c", "http.followRedirects=false",
	}
	base := "https://github.com/" + owner + "/" + repo
	for _, raw := range []string{base, base + ".git"} {
		prefix := "http." + raw + "."
		for _, v := range []string{
			"proxy=", "sslVerify=true", "sslCAInfo=", "sslCAPath=",
			"extraHeader=", "followRedirects=false",
		} {
			opts = append(opts, "-c", prefix+v)
		}
	}
	return opts, nil
}

// trustedGitPushCommandArgs builds a scoped credentialed push invocation.
// Only the selected repository's HTTPS destination receives the token.
func trustedGitPushCommandArgs(workspacePath, branch, owner, repo string) ([]string, error) {
	if strings.TrimSpace(workspacePath) == "" || strings.TrimSpace(branch) == "" {
		return nil, errors.New("git workspace and branch required")
	}
	transport, err := trustedGitTransportArgs(owner, repo)
	if err != nil {
		return nil, err
	}
	args := []string{"-C", workspacePath}
	args = append(args, transport...)
	return append(args, "-c", "core.hooksPath=/dev/null", "-c", "credential.helper=",
		"push", "--", "origin", branch), nil
}
