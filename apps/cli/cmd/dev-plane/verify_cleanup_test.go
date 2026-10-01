package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ai-dev-control-plane/verifier"
)

func TestRunVerifyWithFailsWhenRuntimeCleanupFails(t *testing.T) {
	source, head := initVerifySourceRepo(t)
	runtime := &fakeVerifyRuntime{
		candidate: head,
		config: []byte(`version: 1
verification:
  quality:
    command: make quality
work:
  isolation: container
  max_parallel_cost: 1
`),
	}
	factory := func(verifyOptions) (verifier.WorkspaceRuntime, func() error, error) {
		return runtime, func() error { return errors.New("tmpfs cleanup failed") }, nil
	}

	var output bytes.Buffer
	err := runVerifyWith(context.Background(), []string{
		"--provider=local",
		"--source=" + source,
		"--repository=dporkka/example",
		"--candidate-sha=" + head,
		"--base-sha=" + head,
		"--gate=quality",
		"--evidence-out=" + filepath.Join(t.TempDir(), "evidence.json"),
	}, &output, factory)
	if err == nil || !strings.Contains(err.Error(), "runtime cleanup") || !strings.Contains(err.Error(), "tmpfs cleanup failed") {
		t.Fatalf("runVerifyWith() error = %v, want runtime cleanup failure", err)
	}
	if !strings.Contains(output.String(), `"status":"failed"`) {
		t.Fatalf("verify output did not fail closed: %s", output.String())
	}
}
