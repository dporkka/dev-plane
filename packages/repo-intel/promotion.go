package repointel

import "fmt"

// ScenarioBenchmark is the scored backend comparison for one benchmark scenario.
type ScenarioBenchmark struct {
	ID     string
	Scores []BenchmarkScore
}

// PromotionPolicy defines the quality floor for replacing a baseline backend.
// Latency and token cost are intentionally not blockers: they are only relevant
// after retrieval quality and reliability are at least equivalent.
type PromotionPolicy struct {
	MinRecall float64
	MinF1     float64
	Epsilon   float64
}

// DefaultPromotionPolicy is deliberately conservative for code-change safety.
func DefaultPromotionPolicy() PromotionPolicy {
	return PromotionPolicy{
		MinRecall: 0.80,
		MinF1:     0.75,
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

// EvaluatePromotion enforces a quality-first replacement gate. A candidate must
// succeed on every scenario, clear the absolute quality floor, and avoid any
// per-scenario recall or F1 regression against a successful baseline result.
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
		if !baselineScore.Failed {
			baselineF1Total += baselineScore.F1
			baselineCount++
		}

		if candidateScore.Failed {
			detail := candidateScore.Error
			if detail == "" {
				detail = "unspecified error"
			}
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: candidate failed: %s", scenario.ID, detail))
			continue
		}

		if candidateScore.Recall+policy.Epsilon < policy.MinRecall {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: candidate recall %.3f is below minimum recall %.3f",
					scenario.ID, candidateScore.Recall, policy.MinRecall))
		}
		if candidateScore.F1+policy.Epsilon < policy.MinF1 {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: candidate F1 %.3f is below minimum F1 %.3f",
					scenario.ID, candidateScore.F1, policy.MinF1))
		}

		// A failed baseline is evidence in favor of the candidate, not a reason to
		// waive the candidate's absolute quality floor. There is no meaningful
		// baseline quality score to compare for this scenario.
		if baselineScore.Failed {
			continue
		}
		if candidateScore.Recall+policy.Epsilon < baselineScore.Recall {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: recall regression %.3f < baseline %.3f",
					scenario.ID, candidateScore.Recall, baselineScore.Recall))
		}
		if candidateScore.F1+policy.Epsilon < baselineScore.F1 {
			decision.Reasons = append(decision.Reasons,
				fmt.Sprintf("%s: F1 regression %.3f < baseline %.3f",
					scenario.ID, candidateScore.F1, baselineScore.F1))
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
