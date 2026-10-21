package handlers

import (
	"encoding/base64"
	"os/exec"
	"testing"
)

// TEMPORARY CI-only gofmt snapshot fixture; remove before promotion.
func TestGofmtSnapshotForCI(t *testing.T) {
	for _, name := range []string{
		"repository_completion_verifier.go",
		"repository_completion_verifier_test.go",
		"run_verification_gate_test.go",
	} {
		result, err := exec.Command("gofmt", name).Output()
		if err != nil {
			t.Fatalf("cannot format %s: %v", name, err)
		}
		t.Errorf("GOFMT_SNAPSHOT_BEGIN:%s:%s:GOFMT_SNAPSHOT_END", name, base64.StdEncoding.EncodeToString(result))
	}
}
