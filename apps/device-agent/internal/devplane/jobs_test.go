package devplane

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJobRunsAllowedGitStatusAndTracksOutput(t *testing.T) {
	dir := t.TempDir()
	initGit(t, dir)
	os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello"), 0600)
	runner := NewJobRunner(4)
	job, err := runner.Start(context.Background(), dir, "git-status")
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Wait(job.ID, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "completed" || !strings.Contains(result.Output, "new.txt") {
		t.Fatalf("job: %#v", result)
	}
}

func TestJobRejectsUnapprovedCommandAndNoShell(t *testing.T) {
	dir := t.TempDir()
	runner := NewJobRunner(2)
	for _, command := range []string{"bash", "sh -c touch /tmp/pwned", "git-status;touch /tmp/pwned", "go-test"} {
		if _, err := runner.Start(context.Background(), dir, command); err == nil {
			t.Errorf("accepted %q", command)
		}
	}
}

func initGit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, b)
	}
}

func TestConcurrentCancellationAndCompletion(t *testing.T) {
	dir := t.TempDir()
	initGit(t, dir)
	runner := NewJobRunner(4)
	defer runner.Shutdown()
	for i := 0; i < 80; i++ {
		job, err := runner.Start(context.Background(), dir, "git-status")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { defer close(done); _ = runner.Cancel(job.ID) }()
		if _, err := runner.Wait(job.ID, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		<-done
	}
}
