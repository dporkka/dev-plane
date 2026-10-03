package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	repointel "github.com/ai-dev-control-plane/repo-intel"
)

type inputRun struct {
	Backend      repointel.Backend `json:"backend"`
	Results      []string          `json:"results"`
	LatencyMS    int64             `json:"latency_ms"`
	OutputTokens int               `json:"output_tokens"`
	Error        string            `json:"error,omitempty"`
}

type inputScenario struct {
	ID       string     `json:"id"`
	Expected []string   `json:"expected"`
	Runs     []inputRun `json:"runs"`
}

type inputSuite struct {
	Scenarios []inputScenario `json:"scenarios"`
}

type outputDecision struct {
	Candidate      repointel.Backend `json:"candidate"`
	Baseline       repointel.Backend `json:"baseline"`
	Promote        bool              `json:"promote"`
	Reasons        []string          `json:"reasons"`
	CandidateAvgF1 float64           `json:"candidate_avg_f1"`
	BaselineAvgF1  float64           `json:"baseline_avg_f1"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: codeintel-promote <suite.json>")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatalf("read input: %v", err)
	}

	var input inputSuite
	if err := json.Unmarshal(data, &input); err != nil {
		fatalf("decode input: %v", err)
	}
	if len(input.Scenarios) == 0 {
		fatalf("decode input: scenarios must not be empty")
	}

	scenarios := make([]repointel.ScenarioBenchmark, 0, len(input.Scenarios))
	for _, scenario := range input.Scenarios {
		if scenario.ID == "" {
			fatalf("decode input: scenario id is required")
		}
		if len(scenario.Expected) == 0 {
			fatalf("decode input: scenario %s has no expected results", scenario.ID)
		}
		runs := make([]repointel.BackendRun, 0, len(scenario.Runs))
		for _, run := range scenario.Runs {
			runs = append(runs, repointel.BackendRun{
				Backend:      run.Backend,
				Results:      run.Results,
				Latency:      time.Duration(run.LatencyMS) * time.Millisecond,
				OutputTokens: run.OutputTokens,
				Error:        run.Error,
			})
		}
		scenarios = append(scenarios, repointel.ScenarioBenchmark{
			ID:     scenario.ID,
			Scores: repointel.RankRuns(scenario.Expected, runs),
		})
	}

	decision := repointel.EvaluatePromotion(
		repointel.BackendCodebaseMemory,
		repointel.BackendGitNexus,
		scenarios,
		repointel.DefaultPromotionPolicy(),
	)
	output := outputDecision{
		Candidate:      decision.Candidate,
		Baseline:       decision.Baseline,
		Promote:        decision.Promote,
		Reasons:        decision.Reasons,
		CandidateAvgF1: decision.CandidateAvgF1,
		BaselineAvgF1:  decision.BaselineAvgF1,
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fatalf("encode output: %v", err)
	}
	os.Exit(decisionExitCode(decision.Promote))
}

func decisionExitCode(promote bool) int {
	if promote {
		return 0
	}
	return 3
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
