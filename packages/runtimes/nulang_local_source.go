package runtimes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// prepareNulangLocalCheckout copies an already-authenticated local repository
// checkout into a disposable trusted staging directory before it is archived
// and transferred to Nulang Cloud. The source checkout must already be at the
// immutable commit requested by the caller.
//
// The clone explicitly disables hardlinks so the staging copy cannot share Git
// object files with the CI checkout. The staged origin is either replaced with
// a credential-free provenance URL or removed entirely, preventing local host
// paths and clone credentials from crossing the runtime trust boundary.
func prepareNulangLocalCheckout(ctx context.Context, sourceDir, expectedHead, provenanceCloneURL string) (repoDir, preparedHead string, cleanup func(), err error) {
	sourceDir = strings.TrimSpace(sourceDir)
	if sourceDir == "" {
		return "", "", nil, errors.New("local source directory is required")
	}
	absoluteSource, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve local source directory: %w", err)
	}

	actualHead, err := gitOutputContext(ctx, absoluteSource, "rev-parse", "HEAD")
	if err != nil {
		return "", "", nil, fmt.Errorf("resolve local source HEAD: %w", err)
	}
	expectedHead = strings.TrimSpace(expectedHead)
	if expectedHead == "" {
		expectedHead = actualHead
	}
	if actualHead != expectedHead {
		return "", "", nil, fmt.Errorf("source HEAD mismatch: expected %s got %s", expectedHead, actualHead)
	}

	stagingDir, err := os.MkdirTemp("", "devplane-nulang-local-source-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create local source staging directory: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(stagingDir) }
	failed := true
	defer func() {
		if failed {
			cleanup()
		}
	}()

	repoDir = filepath.Join(stagingDir, "repo")
	clone := exec.CommandContext(ctx, "git", "clone", "--local", "--no-hardlinks", absoluteSource, repoDir)
	if output, cloneErr := clone.CombinedOutput(); cloneErr != nil {
		return "", "", nil, fmt.Errorf("clone local source checkout: %w (output: %s)", cloneErr, strings.TrimSpace(string(output)))
	}

	checkout := exec.CommandContext(ctx, "git", "-C", repoDir, "checkout", "--detach", expectedHead)
	if output, checkoutErr := checkout.CombinedOutput(); checkoutErr != nil {
		return "", "", nil, fmt.Errorf("checkout immutable local source HEAD: %w (output: %s)", checkoutErr, strings.TrimSpace(string(output)))
	}

	provenanceCloneURL = strings.TrimSpace(provenanceCloneURL)
	if provenanceCloneURL == "" {
		removeOrigin := exec.CommandContext(ctx, "git", "-C", repoDir, "remote", "remove", "origin")
		if output, removeErr := removeOrigin.CombinedOutput(); removeErr != nil {
			return "", "", nil, fmt.Errorf("remove host-local git origin: %w (output: %s)", removeErr, strings.TrimSpace(string(output)))
		}
	} else {
		cleanURL := credentialFreeCloneURL(provenanceCloneURL)
		if strings.TrimSpace(cleanURL) == "" {
			return "", "", nil, errors.New("credential-free provenance clone URL is empty")
		}
		setOrigin := exec.CommandContext(ctx, "git", "-C", repoDir, "remote", "set-url", "origin", cleanURL)
		if output, setErr := setOrigin.CombinedOutput(); setErr != nil {
			return "", "", nil, fmt.Errorf("sanitize staged git origin: %w (output: %s)", setErr, strings.TrimSpace(string(output)))
		}
	}

	preparedHead, err = gitOutputContext(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", nil, fmt.Errorf("verify staged source HEAD: %w", err)
	}
	if preparedHead != expectedHead {
		return "", "", nil, fmt.Errorf("staged source HEAD mismatch: expected %s got %s", expectedHead, preparedHead)
	}

	failed = false
	return repoDir, preparedHead, cleanup, nil
}

func gitOutputContext(ctx context.Context, repoDir string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", repoDir}, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}
