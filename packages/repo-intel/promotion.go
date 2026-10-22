package repointel

import "fmt"

// ScenarioBenchmark is the scored backend comparison for one benchmark scenario.
type ScenarioBenchmark struct {
	ID     string
	Scores []BenchmarkScore
}

// PromotionPolicy defines the required-core-path recall floor for replacing a
// baseline backend. The corpus intentionally is not an exhaustive list of every
// relevant file, so precision/F1 remain diagnostic rather than safety gates.
type PromotionPolicy struct {
	MinRecall float64
	Epsilon   float64
}

// DefaultPromotionPolicy requires every curated core path on every scenario.
func DefaultPromotionPolicy() PromotionPolicy {
	return PromotionPolicy{
		MinRecall: 1.0,
		Epsilon:   1e-9,
	}
}

// PromotionDecision explains whether a candidate backend is safe to promote.
type PromotionDecision struct {
	Candidate      Backend
	Baseline       Backend
	Promote        bool
	Reasons        []string
	CandidateAvgF1 float64
	BaselineAvgF1  float64
}

// EvaluatePromotion enforces a required-recall-first replacement gate. Both
// candidate and baseline must successfully complete every scenario so the
// comparison itself is valid. The candidate must then clear the absolute recall
// floor and avoid any per-scenario recall regression against the baseline.
// F1 is retained for observability but does not block promotion because
// additional useful files can lower precision against a deliberately
// non-exhaustive core-path corpus.
func EvaluatePromotion(
	candidate Backend,
	baseline Backend,
	scenarios []ScenarioBenchmark,
	policy PromotionPolicy,
) PromotionDecision {
	decision := PromotionDecision{
		Candidate: candidate,
		Baseline:  baseline,
		Promote:   false,
		Reasons:   []string{},
	}
	if len(scenarios) == 0 {
		decision.Reasons = append(decision.Reasons, "no benchmark scenarios were provided")
		return decision
	}

	candidateF1Total := 0.0
	baselineF1Total := 0.0
	candidateCount := 0
	baselineCount := 0

	for _, scenario := range scenarios {
		candidateScore, candidateOK := scoreForBackend(scenario.Scores, candidate)
		baselineScore, baselineOK := scoreForBackend(scenario.Scores, baseline)

		if !candidateOK {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: missing candidate %s result", scenario.ID, candidate))
		}
		if !baselineOK {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: missing baseline %s result", scenario.ID, baseline))
		}
		if !candidateOK || !baselineOK {
			continue
		}

		candidateF1Total += candidateScore.F1
		candidateCount++
		baselineF1Total += baselineScore.F1
		baselineCount++

		if candidateScore.Failed {
			detail := candidateScore.Error
			if detail == "" {
				detail = "unspecified error"
			}
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: candidate failed: %s", scenario.ID, detail))
		}
		if baselineScore.Failed {
			detail := baselineScore.Error
			if detail == "" {
				detail = "unspecified error"
			}
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: baseline failed: %s", scenario.ID, detail))
		}
		if candidateScore.Failed || baselineScore.Failed {
			continue
		}

		if candidateScore.Recall+policy.Epsilon < policy.MinRecall {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: candidate recall %.3f is below minimum recall %.3f",
					scenario.ID, candidateScore.Recall, policy.MinRecall))
		}
		if candidateScore.Recall+policy.Epsilon < baselineScore.Recall {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: recall regression %.3f < baseline %.3f",
					scenario.ID, candidateScore.Recall, baselineScore.Recall))
		}
	}

	if candidateCount > 0 {
		decision.CandidateAvgF1 = candidateF1Total / float64(candidateCount)
	}
	if baselineCount > 0 {
		decision.BaselineAvgF1 = baselineF1Total / float64(baselineCount)
	}
	decision.Promote = len(decision.Reasons) == 0
	return decision
}

func scoreForBackend(scores []BenchmarkScore, backend Backend) (BenchmarkScore, bool) {
	for _, score := range scores {
		if score.Backend == backend {
			return score, true
		}
	}
	return BenchmarkScore{}, false
}
