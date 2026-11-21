package verifier

import (
	"context"
	"strings"
	"testing"
)

func TestWoodpeckerRequiresExplicitRerunCount(t *testing.T) {
	f := newWoodpeckerFixture(t)
	delete(f.pipeline, "rerun_count")
	if _, err := wpReader(f).ObserveAttempt(context.Background(), wpQuery()); err == nil {
		t.Fatal("missing rerun_count treated as attempt zero")
	}
}

func TestWoodpeckerRechecksPipelineAfterLoadingConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*wpFixture)
	}{
		{"status changes", func(f *wpFixture) { f.pipeline["status"] = "failure" }},
		{"retry increments", func(f *wpFixture) { f.pipeline["rerun_count"] = 1 }},
		{"worker reassignment", func(f *wpFixture) {
			workflows := f.pipeline["workflows"].([]any)
			workflows[0].(map[string]any)["agent_id"] = 99
		}},
		{"step exit changes", func(f *wpFixture) {
			workflows := f.pipeline["workflows"].([]any)
			steps := workflows[0].(map[string]any)["children"].([]any)
			steps[0].(map[string]any)["exit_code"] = 4
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWoodpeckerFixture(t)
			reads := 0
			f.onPipelineFetch = func() {
				reads++
				if reads == 2 {
					tc.mutate(f)
				}
			}
			if _, err := wpReader(f).ObserveAttempt(context.Background(), wpQuery()); err == nil {
				t.Fatal("mutating pipeline accepted as stable exact attempt")
			}
			if reads != 2 {
				t.Fatalf("expected independent second pipeline fetch, got %d", reads)
			}
		})
	}
}

func TestWoodpeckerRejectsInconsistentSuccessfulStep(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"exit missing", func(x map[string]any) { delete(x, "exit_code") }},
		{"nonzero successful exit", func(x map[string]any) { x["exit_code"] = 4 }},
		{"start missing", func(x map[string]any) { delete(x, "started") }},
		{"finish missing", func(x map[string]any) { delete(x, "finished") }},
		{"finish before start", func(x map[string]any) { x["finished"] = 1600000000 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWoodpeckerFixture(t)
			workflow := f.pipeline["workflows"].([]any)[0].(map[string]any)
			step := workflow["children"].([]any)[0].(map[string]any)
			tc.mutate(step)
			if _, err := wpReader(f).ObserveAttempt(context.Background(), wpQuery()); err == nil {
				t.Fatalf("success without completed zero-exit execution accepted: %+v", step)
			}
		})
	}
}

func TestWoodpeckerExposesObservedExitCodeWithoutAttesting(t *testing.T) {
	f := newWoodpeckerFixture(t)
	obs, err := wpReader(f).ObserveAttempt(context.Background(), wpQuery())
	if err != nil {
		t.Fatal(err)
	}
	if obs.ExitCode == nil || *obs.ExitCode != 0 {
		t.Fatalf("lost observed exit code: %+v", obs)
	}
	r, err := wpReader(f).FetchAttempt(context.Background(), wpQuery())
	if err == nil || !strings.Contains(err.Error(), "assignment") || r.HeadSHA != "" {
		t.Fatalf("diagnostic exit code was promoted to release attestation: %+v %v", r, err)
	}
}
