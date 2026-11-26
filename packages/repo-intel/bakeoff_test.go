package repointel

import (
	"testing"
	"time"
)

func TestScoreRunComputesRetrievalQuality(t *testing.T) {
	score := ScoreRun([]string{"a", "b", "c"}, BackendRun{
		Backend:      BackendCodebaseMemory,
		Results:      []string{"a", "b", "x"},
		Latency:      120 * time.Millisecond,
		OutputTokens: 900,
	})

	assertFloatNear(t, score.Precision, 2.0/3.0)
	assertFloatNear(t, score.Recall, 2.0/3.0)
	assertFloatNear(t, score.F1, 2.0/3.0)
	if score.Latency != 120*time.Millisecond {
		t.Fatalf("latency = %s", score.Latency)
	}
	if score.OutputTokens != 900 {
		t.Fatalf("tokens = %d", score.OutputTokens)
	}
}

func TestScoreRunNormalizesAndDeduplicatesResults(t *testing.T) {
	score := ScoreRun([]string{" Src/Foo.go ", "Bar"}, BackendRun{
		Backend: BackendGitNexus,
		Results: []string{"src/foo.go", "SRC/FOO.GO", "bar"},
	})

	assertFloatNear(t, score.Precision, 1)
	assertFloatNear(t, score.Recall, 1)
	assertFloatNear(t, score.F1, 1)
}

func TestRankRunsPrefersRequiredRecallBeforePrecision(t *testing.T) {
	expected := []string{"a", "b", "c", "d"}
	ranked := RankRuns(expected, []BackendRun{
		{
			Backend: BackendGitNexus,
			Results: []string{"a", "b", "c"},
			Latency: 10 * time.Millisecond,
		},
		{
			Backend: BackendCodebaseMemory,
			Results: []string{"a", "b", "c", "d", "extra-1", "extra-2", "extra-3", "extra-4"},
			Latency: 200 * time.Millisecond,
		},
	})

	if ranked[0].Backend != BackendCodebaseMemory {
		t.Fatalf("winner = %q, want backend with complete required-file recall", ranked[0].Backend)
	}
	if ranked[0].Recall != 1 {
		t.Fatalf("winner recall = %f, want 1", ranked[0].Recall)
	}
	if ranked[0].F1 >= ranked[1].F1 {
		t.Fatalf("test setup requires complete-recall backend to have lower F1: %#v", ranked)
	}
}

func TestRankRunsUsesF1ThenLatencyThenTokensAfterRecallTie(t *testing.T) {
	expected := []string{"a", "b"}
	ranked := RankRuns(expected, []BackendRun{
		{Backend: BackendGitNexus, Results: []string{"a", "b"}, Latency: 250 * time.Millisecond, OutputTokens: 500},
		{Backend: BackendCodebaseMemory, Results: []string{"a", "b"}, Latency: 150 * time.Millisecond, OutputTokens: 900},
		{Backend: BackendZoekt, Results: []string{"a"}, Latency: 10 * time.Millisecond, OutputTokens: 100},
	})

	if len(ranked) != 3 {
		t.Fatalf("ranked len = %d", len(ranked))
	}
	if ranked[0].Backend != BackendCodebaseMemory {
		t.Fatalf("winner = %q, want %q", ranked[0].Backend, BackendCodebaseMemory)
	}
	if ranked[2].Backend != BackendZoekt {
		t.Fatalf("last = %q, want lower-recall backend %q", ranked[2].Backend, BackendZoekt)
	}
}

func TestRankRunsPlacesErrorsLast(t *testing.T) {
	ranked := RankRuns([]string{"a"}, []BackendRun{
		{Backend: BackendCodebaseMemory, Results: []string{"a"}, Latency: time.Second, OutputTokens: 1000},
		{Backend: BackendGitNexus, Results: []string{"a"}, Latency: time.Millisecond, OutputTokens: 1, Error: "stale index"},
	})

	if ranked[0].Backend != BackendCodebaseMemory {
		t.Fatalf("winner = %q, expected successful backend", ranked[0].Backend)
	}
	if !ranked[1].Failed {
		t.Fatalf("error run should be marked failed: %#v", ranked[1])
	}
}

func assertFloatNear(t *testing.T, got, want float64) {
	t.Helper()
	const epsilon = 1e-9
	if got < want-epsilon || got > want+epsilon {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}
