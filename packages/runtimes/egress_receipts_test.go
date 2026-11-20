package runtimes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerCreateWorkspaceMountsHostOwnedReadonlyReceipts(t *testing.T) {
	runner := &fakeRunner{}
	provider := &DockerProvider{baseDir: t.TempDir(), image: "test-image", runner: runner, sessions: map[string]*dockerSession{}}
	sess, err := provider.CreateWorkspace(context.Background(), CreateRequest{RepositoryID: "repo", CloneURL: "https://example.invalid/repo", BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(provider.baseDir, sess.ID, "egress-receipts")
	stat, err := os.Lstat(dir)
	if err != nil || !stat.IsDir() || stat.Mode().Perm() != 0700 {
		t.Fatalf("host receipt directory must be private: %v %v", stat, err)
	}
	run := findCall(runner.calls, "docker", "run")
	if run == nil || !containsArg(run.args, dir+":/run/dev-plane/egress:ro,Z") {
		t.Fatalf("missing read-only receipt mount: %+v", run)
	}
}

type receiptInspectRunner struct {
	fakeRunner
	source   string
	writable bool
}

func (r *receiptInspectRunner) Run(ctx context.Context, name string, args []string, opts commandOptions) (commandOutput, error) {
	if name == "docker" && len(args) > 2 && args[0] == "inspect" && args[1] == "-f" && strings.Contains(args[2], ".Mounts") {
		if r.source == "" {
			return commandOutput{Stdout: "\n"}, nil
		}
		readWrite := "false"
		if r.writable {
			readWrite = "true"
		}
		return commandOutput{Stdout: r.source + "|" + readWrite + "\n"}, nil
	}
	return r.fakeRunner.Run(ctx, name, args, opts)
}

func TestDockerReceiptWriteIsAtomicExclusiveAndRejectsPoisonedWorkspace(t *testing.T) {
	base := t.TempDir()
	session := "test-session"
	dir := filepath.Join(base, session, "egress-receipts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	runner := &receiptInspectRunner{source: dir}
	provider := &DockerProvider{baseDir: base, runner: runner, sessions: map[string]*dockerSession{
		session: {id: session, container: "dev-plane-" + session, status: "ready"},
	}}
	// Malicious paths inside a writable workspace must be irrelevant to host
	// receipt publication, because the target is outside the workspace volume.
	poison := filepath.Join(base, "agent-workspace")
	if err := os.MkdirAll(poison, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(poison, ".dev-plane")); err != nil {
		t.Fatal(err)
	}

	if err := provider.WriteEgressReceipt(context.Background(), session, "receipt-1", []byte("trusted-response")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "receipt-1.json"))
	if err != nil || string(got) != "trusted-response" {
		t.Fatalf("incorrect published artifact: %q %v", got, err)
	}
	if err := provider.WriteEgressReceipt(context.Background(), session, "receipt-1", []byte("overwrite")); err == nil {
		t.Fatal("overwrote existing receipt")
	}
	got, _ = os.ReadFile(filepath.Join(dir, "receipt-1.json"))
	if string(got) != "trusted-response" {
		t.Fatal("receipt mutated by replay")
	}
	for _, id := range []string{"../escape", "evil/name", "", "a.json/../../etc"} {
		if err := provider.WriteEgressReceipt(context.Background(), session, id, []byte("x")); err == nil {
			t.Errorf("allowed invalid receipt ID %q", id)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(base, session, ".pending-*")); len(matches) != 0 {
		t.Fatalf("unfinished staging files: %v", matches)
	}
}

func TestDockerReceiptWriterFailsClosedWhenReadonlyMountMissingOrWritable(t *testing.T) {
	base := t.TempDir()
	session := "test-session"
	dir := filepath.Join(base, session, "egress-receipts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	runner := &receiptInspectRunner{}
	provider := &DockerProvider{baseDir: base, runner: runner, sessions: map[string]*dockerSession{session: {id: session, container: "dev-plane-" + session}}}
	if err := provider.WriteEgressReceipt(context.Background(), session, "receipt-1", []byte("secret")); err == nil {
		t.Fatal("wrote without the read-only mount")
	}
	runner.source = dir
	runner.writable = true
	if err := provider.WriteEgressReceipt(context.Background(), session, "receipt-1", []byte("secret")); err == nil {
		t.Fatal("wrote while mount writable")
	}
	runner.source = filepath.Join(base, "other")
	runner.writable = false
	if err := provider.WriteEgressReceipt(context.Background(), session, "receipt-1", []byte("secret")); err == nil {
		t.Fatal("wrote to unexpected mount")
	}
	if _, err := os.Stat(filepath.Join(dir, "receipt-1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unauthorized artifact exists: %v", err)
	}
}

func TestDockerReceiptWriterRefusesPreexistingSymlinkedReceipt(t *testing.T) {
	base := t.TempDir()
	session := "test-session"
	dir := filepath.Join(base, session, "egress-receipts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "receipt-1.json")); err != nil {
		t.Fatal(err)
	}
	runner := &receiptInspectRunner{source: dir}
	provider := &DockerProvider{baseDir: base, runner: runner, sessions: map[string]*dockerSession{
		session: {id: session, container: "dev-plane-" + session, status: "ready"},
	}}
	if err := provider.WriteEgressReceipt(context.Background(), session, "receipt-1", []byte("secret")); err == nil {
		t.Fatal("followed or overwrote symlinked receipt")
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("changed outside file: %q %v", got, err)
	}
}

func TestDockerReceiptWriterRejectsSymlinkedHostDirectory(t *testing.T) {
	base := t.TempDir()
	session := "test-session"
	path := filepath.Join(base, session)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(path, "egress-receipts")); err != nil {
		t.Fatal(err)
	}
	runner := &receiptInspectRunner{source: filepath.Join(path, "egress-receipts")}
	provider := &DockerProvider{baseDir: base, runner: runner, sessions: map[string]*dockerSession{session: {id: session, container: "dev-plane-" + session}}}
	if err := provider.WriteEgressReceipt(context.Background(), session, "receipt-1", []byte("secret")); err == nil {
		t.Fatal("followed host directory symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "receipt-1.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrote outside artifact directory: %v", err)
	}
}
