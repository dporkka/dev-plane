package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	ID         string     `json:"id"`
	Repository string     `json:"repository"`
	Revision   string     `json:"revision"`
	Expected   []string   `json:"expected"`
	Runs       []inputRun `json:"runs"`
}

type inputRepository struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

type inputCorpus struct {
	Version int    `json:"version"`
	SHA256  string `json:"sha256"`
	Path    string `json:"path,omitempty"`
}

type inputSuite struct {
	ProvenanceVersion int                        `json:"provenance_version"`
	Corpus            inputCorpus                `json:"corpus"`
	Tools             map[string]string          `json:"tools"`
	Repositories      map[string]inputRepository `json:"repositories"`
	Scenarios         []inputScenario            `json:"scenarios"`
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
	if err := validatePromotionProvenance(input); err != nil {
		fatalf("provenance validation: %v", err)
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

func validatePromotionProvenance(input inputSuite) error {
	if input.ProvenanceVersion < 2 {
		return fmt.Errorf("provenance_version %d is too old; require >= 2", input.ProvenanceVersion)
	}
	if input.Corpus.Version <= 0 {
		return fmt.Errorf("corpus version is required")
	}
	if !isSHA256(input.Corpus.SHA256) {
		return fmt.Errorf("corpus sha256 is invalid")
	}

	for _, backend := range []string{string(repointel.BackendCodebaseMemory), string(repointel.BackendGitNexus)} {
		version, ok := input.Tools[backend]
		if !ok || invalidProvenanceValue(version) {
			return fmt.Errorf("%s tool version is missing or invalid", backend)
		}
	}

	for _, scenario := range input.Scenarios {
		if scenario.Repository == "" {
			return fmt.Errorf("scenario %q repository provenance is missing", scenario.ID)
		}
		if invalidProvenanceValue(scenario.Revision) {
			return fmt.Errorf("scenario %q revision is missing or invalid", scenario.ID)
		}
		repository, ok := input.Repositories[scenario.Repository]
		if !ok {
			return fmt.Errorf("scenario %q repository provenance for %q is missing", scenario.ID, scenario.Repository)
		}
		if invalidProvenanceValue(repository.Revision) {
			return fmt.Errorf("repository provenance for %q has invalid revision", scenario.Repository)
		}
		if scenario.Revision != repository.Revision {
			return fmt.Errorf("scenario %q revision %q does not match repository provenance %q", scenario.ID, scenario.Revision, repository.Revision)
		}
	}
	return nil
}

func invalidProvenanceValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "unknown") {
		return true
	}
	return strings.HasPrefix(strings.ToLower(value), "error:")
}

func isSHA256(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
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
