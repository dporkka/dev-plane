package repointel

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluatePromotionAllowsCompleteRecallCandidate(t *testing.T) {
	scenarios := []ScenarioBenchmark{
		{
			ID: "proposal-flow",
			Scores: []BenchmarkScore{
				{Backend: BackendCodebaseMemory, Recall: 1.0, Precision: 0.9, F1: 0.947, Latency: 250 * time.Millisecond},
				{Backend: BackendGitNexus, Recall: 0.8, Precision: 0.8, F1: 0.8, Latency: 120 * time.Millisecond},
			},
		},
		{
			ID: "mir-callers",
			Scores: []BenchmarkScore{
				{Backend: BackendCodebaseMemory, Recall: 1.0, Precision: 1.0, F1: 1.0, Latency: 300 * time.Millisecond},
				{Backend: BackendGitNexus, Recall: 1.0, Precision: 0.8, F1: 0.889, Latency: 100 * time.Millisecond},
			},
		},
	}

	decision := EvaluatePromotion(
		BackendCodebaseMemory,
		BackendGitNexus,
		scenarios,
		DefaultPromotionPolicy(),
	)
	if !decision.Promote {
		t.Fatalf("expected promotion, got reasons: %#v", decision.Reasons)
	}
}

func TestEvaluatePromotionAllowsLowerF1WhenRequiredRecallIsComplete(t *testing.T) {
	scenarios := []ScenarioBenchmark{
		{
			ID: "broad-but-complete",
			Scores: []BenchmarkScore{
				{Backend: BackendCodebaseMemory, Recall: 1.0, Precision: 0.25, F1: 0.4, Latency: 300 * time.Millisecond},
				{Backend: BackendGitNexus, Recall: 1.0, Precision: 0.9, F1: 0.947, Latency: 100 * time.Millisecond},
			},
		},
	}

	decision := EvaluatePromotion(
		BackendCodebaseMemory,
		BackendGitNexus,
		scenarios,
		DefaultPromotionPolicy(),
	)
	if !decision.Promote {
		t.Fatalf("complete required recall should not be blocked by non-exhaustive-set F1: %#v", decision.Reasons)
	}
}

func TestEvaluatePromotionRejectsRecallRegressionEvenWhenCandidateIsFaster(t *testing.T) {
	scenarios := []ScenarioBenchmark{
		{
			ID: "proposal-flow",
			Scores: []BenchmarkScore{
				{Backend: BackendCodebaseMemory, Recall: 0.8, Precision: 1.0, F1: 0.889, Latency: 20 * time.Millisecond},
				{Backend: BackendGitNexus, Recall: 1.0, Precision: 0.8, F1: 0.889, Latency: 200 * time.Millisecond},
			},
		},
	}

	decision := EvaluatePromotion(
		BackendCodebaseMemory,
		BackendGitNexus,
		scenarios,
		DefaultPromotionPolicy(),
	)
	if decision.Promote {
		t.Fatal("expected recall regression to block promotion")
	}
	assertContainsReason(t, decision.Reasons, "recall regression")
}

func TestEvaluatePromotionRejectsFailedCandidate(t *testing.T) {
	scenarios := []ScenarioBenchmark{
		{
			ID: "runtime-boundary",
			Scores: []BenchmarkScore{
				{Backend: BackendCodebaseMemory, Failed: true, Error: "stale index"},
				{Backend: BackendGitNexus, Recall: 1.0, Precision: 1.0, F1: 1.0},
			},
		},
	}

	decision := EvaluatePromotion(
		BackendCodebaseMemory,
		BackendGitNexus,
		scenarios,
		DefaultPromotionPolicy(),
	)
	if decision.Promote {
		t.Fatal("expected failed candidate to block promotion")
	}
	assertContainsReason(t, decision.Reasons, "candidate failed")
}

func TestEvaluatePromotionRejectsCandidateBelowAbsoluteRecallFloor(t *testing.T) {
	policy := DefaultPromotionPolicy()
	policy.MinRecall = 0.9
	scenarios := []ScenarioBenchmark{
		{
			ID: "admission",
			Scores: []BenchmarkScore{
				{Backend: BackendCodebaseMemory, Recall: 0.8, Precision: 1.0, F1: 0.889},
				{Backend: BackendGitNexus, Recall: 0.8, Precision: 1.0, F1: 0.889},
			},
		},
	}

	decision := EvaluatePromotion(
		BackendCodebaseMemory,
		BackendGitNexus,
		scenarios,
		policy,
	)
	if decision.Promote {
		t.Fatal("expected absolute recall floor to block equally incomplete backends")
	}
	assertContainsReason(t, decision.Reasons, "below minimum recall")
}

func TestEvaluatePromotionRejectsMissingBaselineOrCandidate(t *testing.T) {
	decision := EvaluatePromotion(
		BackendCodebaseMemory,
		BackendGitNexus,
		[]ScenarioBenchmark{{
			ID:     "missing-baseline",
			Scores: []BenchmarkScore{{Backend: BackendCodebaseMemory, Recall: 1, Precision: 1, F1: 1}},
		}},
		DefaultPromotionPolicy(),
	)
	if decision.Promote {
		t.Fatal("expected missing comparison backend to block promotion")
	}
	assertContainsReason(t, decision.Reasons, "missing baseline")
}

func assertContainsReason(t *testing.T, reasons []string, want string) {
	t.Helper()
	want = strings.ToLower(want)
	for _, reason := range reasons {
		if strings.Contains(strings.ToLower(reason), want) {
			return
		}
	}
	t.Fatalf("reasons %#v do not contain %q", reasons, want)
}
