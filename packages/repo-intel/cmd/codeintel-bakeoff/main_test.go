package main

import (
	"testing"
	"time"

	repointel "github.com/ai-dev-control-plane/repo-intel"
)

func TestFormatScoresUsesMillisecondsAndStableJSONShape(t *testing.T) {
	formatted := formatScores([]repointel.BenchmarkScore{{
		Backend:      repointel.BackendCodebaseMemory,
		Precision:    1,
		Recall:       0.5,
		F1:           2.0 / 3.0,
		Latency:      150 * time.Millisecond,
		OutputTokens: 321,
	}})

	if len(formatted) != 1 {
		t.Fatalf("formatted len = %d, want 1", len(formatted))
	}
	if formatted[0].LatencyMS != 150 {
		t.Fatalf("latency_ms = %d, want 150", formatted[0].LatencyMS)
	}
	if formatted[0].Backend != repointel.BackendCodebaseMemory {
		t.Fatalf("backend = %q", formatted[0].Backend)
	}
	if formatted[0].OutputTokens != 321 {
		t.Fatalf("output_tokens = %d", formatted[0].OutputTokens)
	}
}
