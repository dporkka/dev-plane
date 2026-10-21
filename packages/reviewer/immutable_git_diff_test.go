package reviewer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitForReviewTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func reviewRepository(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	gitForReviewTest(t, dir, "init", "-q")
	gitForReviewTest(t, dir, "config", "user.name", "Review Test")
	gitForReviewTest(t, dir, "config", "user.email", "review-test@example.invalid")
	path := filepath.Join(dir, "change.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitForReviewTest(t, dir, "add", "change.txt")
	gitForReviewTest(t, dir, "commit", "-qm", "base")
	base := gitForReviewTest(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(path, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitForReviewTest(t, dir, "commit", "-qam", "implementation")
	head := gitForReviewTest(t, dir, "rev-parse", "HEAD")
	return dir, base, head
}

func TestGetGitDiffReviewsCommittedChangeAtExactRevisions(t *testing.T) {
	dir, base, head := reviewRepository(t)
	reviewer := NewReviewer(nil, nil)
	diff, err := reviewer.getGitDiff(context.Background(), dir, base, head)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "-before") || !strings.Contains(diff, "+after") {
		t.Fatalf("committed changes not reviewed: %s", diff)
	}
}

func TestGetGitDiffRejectsMissingOrStaleRevision(t *testing.T) {
	dir, base, head := reviewRepository(t)
	reviewer := NewReviewer(nil, nil)
	for _, tc := range []struct {
		name string
		base string
		head string
	}{
		{"missing base", "", head},
		{"missing head", base, ""},
		{"stale head", head, base},
		{"invalid revision", "main", head},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := reviewer.getGitDiff(context.Background(), dir, tc.base, tc.head); err == nil {
				t.Fatal("expected revision mismatch or invalid provenance to fail closed")
			}
		})
	}
}

func TestGetGitDiffRejectsDirtyWorkspace(t *testing.T) {
	dir, base, head := reviewRepository(t)
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("unreviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	reviewer := NewReviewer(nil, nil)
	if _, err := reviewer.getGitDiff(context.Background(), dir, base, head); err == nil {
		t.Fatal("untracked workspace change must block immutable review")
	}
}

func TestGetGitDiffRejectsEmptyCandidate(t *testing.T) {
	dir, base, _ := reviewRepository(t)
	reviewer := NewReviewer(nil, nil)
	if _, err := reviewer.getGitDiff(context.Background(), dir, base, base); err == nil {
		t.Fatal("empty implementation diff must not be silently approved")
	}
}
