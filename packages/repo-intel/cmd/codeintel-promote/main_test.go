package main

import "testing"

func TestDecisionExitCode(t *testing.T) {
	if got := decisionExitCode(true); got != 0 {
		t.Fatalf("promote=true exit code = %d, want 0", got)
	}
	if got := decisionExitCode(false); got == 0 {
		t.Fatal("promote=false must return a non-zero exit code")
	}
}
