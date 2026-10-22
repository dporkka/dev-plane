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

type inputDocument struct {
	Scenario string     `json:"scenario"`
	Expected []string   `json:"expected"`
	Runs     []inputRun `json:"runs"`
}

type outputScore struct {
	Backend      repointel.Backend `json:"backend"`
	Precision    float64           `json:"precision"`
	Recall       float64           `json:"recall"`
	F1           float64           `json:"f1"`
	LatencyMS    int64             `json:"latency_ms"`
	OutputTokens int               `json:"output_tokens"`
	Failed       bool              `json:"failed"`
	Error        string            `json:"error,omitempty"`
}

type outputDocument struct {
	Scenario string        `json:"scenario"`
	Scores   []outputScore `json:"scores"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: codeintel-bakeoff <results.json>")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatalf("read input: %v", err)
	}

	var input inputDocument
	if err := json.Unmarshal(data, &input); err != nil {
		fatalf("decode input: %v", err)
	}
	if input.Scenario == "" {
		fatalf("decode input: scenario is required")
	}
	if len(input.Expected) == 0 {
		fatalf("decode input: expected must contain at least one result key")
	}
	if len(input.Runs) == 0 {
		fatalf("decode input: runs must contain at least one backend observation")
	}

	runs := make([]repointel.BackendRun, 0, len(input.Runs))
	for _, run := range input.Runs {
		runs = append(runs, repointel.BackendRun{
			Backend:      run.Backend,
			Results:      run.Results,
			Latency:      time.Duration(run.LatencyMS) * time.Millisecond,
			OutputTokens: run.OutputTokens,
			Error:        run.Error,
		})
	}

	output := outputDocument{
		Scenario: input.Scenario,
		Scores:   formatScores(repointel.RankRuns(input.Expected, runs)),
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		fatalf("encode output: %v", err)
	}
}

func formatScores(scores []repointel.BenchmarkScore) []outputScore {
	formatted := make([]outputScore, 0, len(scores))
	for _, score := range scores {
		formatted = append(formatted, outputScore{
			Backend:      score.Backend,
			Precision:    score.Precision,
			Recall:       score.Recall,
			F1:           score.F1,
			LatencyMS:    score.Latency.Milliseconds(),
			OutputTokens: score.OutputTokens,
			Failed:       score.Failed,
			Error:        score.Error,
		})
	}
	return formatted
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
