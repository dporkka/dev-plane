package prfactory

import (
	"context"
	"strings"
	"testing"
)

func TestWorkspaceOriginValidatesEffectivePushDestination(t *testing.T) {
	const trusted = "https://trusted.example/git/acme/widget.git"
	const untrusted = "https://attacker.example/git/acme/widget.git"
	for _, tc := range []struct {
		name      string
		config    [][]string
		wantError string
	}{
		{name: "single approved origin"},
		{name: "explicit approved push URL", config: [][]string{{"remote", "set-url", "--push", "origin", trusted}}},
		{name: "separate push URL", config: [][]string{{"remote", "set-url", "--push", "origin", untrusted}}, wantError: "authority mismatch"},
		{name: "push URL rewrite", config: [][]string{{"config", "--local", "url.https://attacker.example/.pushInsteadOf", "https://trusted.example/"}}, wantError: "authority mismatch"},
		{name: "multiple push destinations", config: [][]string{{"remote", "set-url", "--add", "--push", "origin", trusted}, {"remote", "set-url", "--add", "--push", "origin", untrusted}}, wantError: "exactly one"},
		{name: "query-bearing push URL", config: [][]string{{"remote", "set-url", "--push", "origin", trusted + "?secret=synthetic-secret"}}, wantError: "query"},
		{name: "fragment-bearing push URL", config: [][]string{{"remote", "set-url", "--push", "origin", trusted + "#synthetic-secret"}}, wantError: "fragment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			runOriginGit(t, repo, "init", "--quiet")
			runOriginGit(t, repo, "remote", "add", "origin", trusted)
			for _, args := range tc.config {
				runOriginGit(t, repo, args...)
			}
			err := originCredentialFactory().validateWorkspaceOrigin(context.Background(), repo, originGiteaTarget("https://trusted.example/git"))
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("approved destination refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected %q, got %v", tc.wantError, err)
			}
			if strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("validation error leaked URL credential")
			}
		})
	}
}
