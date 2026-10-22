package devplane

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const maxJobOutput = 65536

type Job struct {
	ID         string     `json:"id"`
	Profile    string     `json:"profile"`
	State      string     `json:"state"`
	Output     string     `json:"output"`
	ExitCode   int        `json:"exitCode"`
	Truncated  bool       `json:"truncated"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type runningJob struct {
	job    Job
	cancel context.CancelFunc
	done   chan struct{}
}

type JobRunner struct {
	mu     sync.Mutex
	jobs   map[string]*runningJob
	limit  int
	active int
}

func NewJobRunner(limit int) *JobRunner {
	if limit < 1 {
		limit = 1
	}
	return &JobRunner{jobs: make(map[string]*runningJob), limit: limit}
}

func jobArgs(profile string) ([]string, error) {
	// Fixed, read-only profiles. No user-controlled command, subcommands or args.
	switch profile {
	case "git-status":
		return []string{"-c", "core.fsmonitor=false", "status", "--porcelain=v1", "--untracked-files=normal"}, nil
	case "git-diff":
		return []string{"-c", "core.fsmonitor=false", "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "HEAD", "--"}, nil
	case "git-log":
		return []string{"-c", "core.fsmonitor=false", "log", "-5", "--format=%h %s"}, nil
	default:
		return nil, errors.New("unsupported execution profile")
	}
}

type limitedOutput struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (w *limitedOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := maxJobOutput - w.buf.Len()
	if len(data) > room {
		w.truncated = true
	}
	if room > 0 {
		w.buf.Write(data[:minInt(room, len(data))])
	}
	return len(data), nil
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func (w *limitedOutput) Snapshot() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String(), w.truncated
}

func (r *JobRunner) Start(_ context.Context, dir, profile string) (Job, error) {
	args, err := jobArgs(profile)
	if err != nil {
		return Job{}, err
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Job{}, err
	}
	id := hex.EncodeToString(idBytes)
	r.mu.Lock()
	if r.active >= r.limit {
		r.mu.Unlock()
		return Job{}, errors.New("too many active jobs")
	}
	if len(r.jobs) >= 100 { // trim completed jobs, never running jobs
		for key, value := range r.jobs {
			if value.job.State != "running" {
				delete(r.jobs, key)
				break
			}
		}
		if len(r.jobs) >= 100 {
			r.mu.Unlock()
			return Job{}, errors.New("job history full")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	item := &runningJob{job: Job{ID: id, Profile: profile, State: "running", ExitCode: -1, StartedAt: time.Now().UTC()}, cancel: cancel, done: make(chan struct{})}
	r.jobs[id] = item
	r.active++
	initial := item.job
	r.mu.Unlock()
	go func() {
		defer cancel()
		output := &limitedOutput{}
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		// No caller environment is inherited: disable global/system git configuration,
		// optional hooks/external diff machinery, and credential prompts.
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C", "LC_ALL=C", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "GIT_EXTERNAL_DIFF=", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			r.finish(id, err, output)
			return
		}
		// Kill the entire process group on timeout/cancellation, not only git.
		finished := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			case <-finished:
			}
		}()
		runErr := cmd.Wait()
		close(finished)
		r.finish(id, runErr, output)
	}()
	return initial, nil
}

func (r *JobRunner) finish(id string, err error, output *limitedOutput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.jobs[id]
	if item == nil {
		return
	}
	item.job.Output, item.job.Truncated = output.Snapshot()
	item.job.State = "completed"
	item.job.ExitCode = 0
	if err != nil {
		item.job.State = "failed"
		item.job.ExitCode = 1
		var e *exec.ExitError
		if errors.As(err, &e) {
			item.job.ExitCode = e.ExitCode()
		} else {
			item.job.Output += "\n" + err.Error()
		}
	}
	at := time.Now().UTC()
	item.job.FinishedAt = &at
	r.active--
	close(item.done)
}

func (r *JobRunner) Status(id string) (Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.jobs[id]
	if !ok {
		return Job{}, fmt.Errorf("unknown job")
	}
	return item.job, nil
}
func (r *JobRunner) Cancel(id string) error {
	r.mu.Lock()
	item, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown job")
	}
	if item.job.State == "running" {
		item.cancel()
	}
	return nil
}
func (r *JobRunner) Wait(id string, timeout time.Duration) (Job, error) {
	r.mu.Lock()
	item, ok := r.jobs[id]
	r.mu.Unlock()
	if !ok {
		return Job{}, errors.New("unknown job")
	}
	select {
	case <-item.done:
		return r.Status(id)
	case <-time.After(timeout):
		return Job{}, errors.New("job still running")
	}
}
func (r *JobRunner) Shutdown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range r.jobs {
		if item.job.State == "running" {
			item.cancel()
		}
	}
}
