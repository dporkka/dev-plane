package repointel

import (
	"sort"
	"strings"
	"time"
)

// BackendRun is one observed backend result for a benchmark scenario.
type BackendRun struct {
	Backend      Backend
	Results      []string
	Latency      time.Duration
	OutputTokens int
	Error        string
}

// BenchmarkScore captures required-result coverage plus precision/cost signals.
// The benchmark corpus is a set of core required paths rather than an exhaustive
// list of every relevant file, so recall is the primary safety signal.
type BenchmarkScore struct {
	Backend      Backend
	Precision    float64
	Recall       float64
	F1           float64
	Latency      time.Duration
	OutputTokens int
	Failed       bool
	Error        string
}

// ScoreRun compares a backend's normalized unique result keys to the expected
// core result keys for a scenario.
func ScoreRun(expected []string, run BackendRun) BenchmarkScore {
	expectedSet := normalizedSet(expected)
	actualSet := normalizedSet(run.Results)

	matches := 0
	for item := range actualSet {
		if _, ok := expectedSet[item]; ok {
			matches++
		}
	}

	precision := ratio(matches, len(actualSet))
	recall := ratio(matches, len(expectedSet))
	f1 := 0.0
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	if len(expectedSet) == 0 && len(actualSet) == 0 {
		precision, recall, f1 = 1, 1, 1
	}

	return BenchmarkScore{
		Backend:      run.Backend,
		Precision:    precision,
		Recall:       recall,
		F1:           f1,
		Latency:      run.Latency,
		OutputTokens: run.OutputTokens,
		Failed:       run.Error != "",
		Error:        run.Error,
	}
}

// RankRuns ranks successful runs by required-result recall first. F1 is a
// secondary relevance/noise signal, followed by latency and output-token cost.
// Failed runs always sort after successful runs.
func RankRuns(expected []string, runs []BackendRun) []BenchmarkScore {
	scores := make([]BenchmarkScore, 0, len(runs))
	for _, run := range runs {
		scores = append(scores, ScoreRun(expected, run))
	}

	sort.SliceStable(scores, func(i, j int) bool {
		a, b := scores[i], scores[j]
		if a.Failed != b.Failed {
			return !a.Failed
		}
		if a.Recall != b.Recall {
			return a.Recall > b.Recall
		}
		if a.F1 != b.F1 {
			return a.F1 > b.F1
		}
		if a.Latency != b.Latency {
			return a.Latency < b.Latency
		}
		if a.OutputTokens != b.OutputTokens {
			return a.OutputTokens < b.OutputTokens
		}
		return a.Backend < b.Backend
	})

	return scores
}

func normalizedSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		set[item] = struct{}{}
	}
	return set
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
