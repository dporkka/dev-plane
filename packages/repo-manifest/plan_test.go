package repomanifest

import (
	"reflect"
	"strings"
	"testing"
)

func TestPlanChecksExpandsReverseDependencies(t *testing.T) {
	manifest, err := Parse([]byte(`{
		"schema_version": 1,
		"commands": {
			"lint": {"run": "make lint"},
			"test": {"run": "make test"}
		},
		"components": {
			"manifest": {
				"paths": ["packages/repo-manifest/**"],
				"checks": {
					"lint": {"run": "cd packages/repo-manifest && go vet ./..."},
					"test": {"run": "cd packages/repo-manifest && go test ./..."}
				}
			},
			"cli": {
				"paths": ["apps/cli/**"],
				"depends_on": ["manifest"],
				"checks": {
					"test": {"run": "cd apps/cli && go test ./..."}
				}
			},
			"api": {
				"paths": ["apps/api/**"],
				"depends_on": ["manifest"],
				"checks": {
					"test": {"run": "cd apps/api && go test ./..."}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	plan, err := manifest.PlanChecks([]string{"packages/repo-manifest/manifest.go"})
	if err != nil {
		t.Fatalf("PlanChecks: %v", err)
	}

	if want := []string{"manifest"}; !reflect.DeepEqual(plan.ChangedComponents, want) {
		t.Fatalf("ChangedComponents = %#v, want %#v", plan.ChangedComponents, want)
	}
	if want := []string{"api", "cli", "manifest"}; !reflect.DeepEqual(plan.AffectedComponents, want) {
		t.Fatalf("AffectedComponents = %#v, want %#v", plan.AffectedComponents, want)
	}

	got := make([]string, 0, len(plan.Checks))
	for _, check := range plan.Checks {
		got = append(got, check.Component+":"+check.Kind+":"+check.Command.Run)
	}
	want := []string{
		"manifest:lint:cd packages/repo-manifest && go vet ./...",
		"api:test:cd apps/api && go test ./...",
		"cli:test:cd apps/cli && go test ./...",
		"manifest:test:cd packages/repo-manifest && go test ./...",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Checks = %#v, want %#v", got, want)
	}
}

func TestPlanChecksUsesFallbackForUnmatchedFiles(t *testing.T) {
	manifest, err := Parse([]byte(`{
		"schema_version": 1,
		"commands": {
			"lint": {"run": "make lint"},
			"test": {"run": "make test"}
		},
		"validation": {
			"fallback_checks": ["lint", "test"]
		},
		"components": {
			"cli": {
				"paths": ["apps/cli/**"],
				"checks": {
					"test": {"run": "cd apps/cli && go test ./..."}
				}
			}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	plan, err := manifest.PlanChecks([]string{"Makefile"})
	if err != nil {
		t.Fatalf("PlanChecks: %v", err)
	}

	got := make([]string, 0, len(plan.Checks))
	for _, check := range plan.Checks {
		got = append(got, check.Kind+":"+check.Command.Run)
	}
	if want := []string{"lint:make lint", "test:make test"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback checks = %#v, want %#v", got, want)
	}
}

func TestParseRejectsUnknownComponentDependency(t *testing.T) {
	_, err := Parse([]byte(`{
		"schema_version": 1,
		"components": {
			"cli": {
				"paths": ["apps/cli/**"],
				"depends_on": ["missing"]
			}
		}
	}`))
	if err == nil || !strings.Contains(err.Error(), "depends_on") {
		t.Fatalf("error = %v, want depends_on validation error", err)
	}
}

func TestParseRejectsComponentDependencyCycle(t *testing.T) {
	_, err := Parse([]byte(`{
		"schema_version": 1,
		"components": {
			"a": {"paths": ["a/**"], "depends_on": ["b"]},
			"b": {"paths": ["b/**"], "depends_on": ["a"]}
		}
	}`))
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v, want cycle validation error", err)
	}
}
