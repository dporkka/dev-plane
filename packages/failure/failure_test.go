package failure

import "testing"

func TestClassifyVerificationFailures(t *testing.T) {
	tests := []struct {
		name        string
		signal      Signal
		category    Category
		retryable   bool
		disposition Disposition
	}{
		{
			name:        "test assertion failure",
			signal:      Signal{Stage: "verification", Evidence: "tests", Detail: "--- FAIL: TestCreateTask"},
			category:    CategoryTest,
			retryable:   false,
			disposition: DispositionFix,
		},
		{
			name:        "typecheck failure",
			signal:      Signal{Stage: "verification", Evidence: "typecheck", Detail: "cannot use string as int"},
			category:    CategoryCode,
			retryable:   false,
			disposition: DispositionFix,
		},
		{
			name:        "transient network failure beats evidence fallback",
			signal:      Signal{Stage: "verification", Evidence: "tests", Detail: "read tcp: connection reset by peer"},
			category:    CategoryInfrastructure,
			retryable:   true,
			disposition: DispositionRetry,
		},
		{
			name:        "resource exhaustion",
			signal:      Signal{Stage: "verification", Evidence: "build", Detail: "write /tmp/build: no space left on device"},
			category:    CategoryResource,
			retryable:   true,
			disposition: DispositionRetryFreshEnvironment,
		},
		{
			name:        "missing executable",
			signal:      Signal{Stage: "verification", Evidence: "tests", Detail: "exec: go: executable file not found in $PATH"},
			category:    CategoryEnvironment,
			retryable:   true,
			disposition: DispositionRetryFreshEnvironment,
		},
		{
			name:        "dependency resolution",
			signal:      Signal{Stage: "verification", Evidence: "build", Detail: "no matching version found for module example.com/missing"},
			category:    CategoryDependency,
			retryable:   false,
			disposition: DispositionFix,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.signal)
			if got.Category != tt.category || got.Retryable != tt.retryable || got.Disposition != tt.disposition {
				t.Fatalf("classification = %+v, want category=%q retryable=%v disposition=%q",
					got, tt.category, tt.retryable, tt.disposition)
			}
		})
	}
}

func TestClassifyControlPlaneFailures(t *testing.T) {
	tests := []struct {
		name        string
		signal      Signal
		category    Category
		retryable   bool
		disposition Disposition
	}{
		{
			name:        "timeout",
			signal:      Signal{Stage: "execution", Detail: "context deadline exceeded"},
			category:    CategoryTimeout,
			retryable:   true,
			disposition: DispositionRetry,
		},
		{
			name:        "configuration",
			signal:      Signal{Stage: "verification", Detail: "load final verification commands: missing project config"},
			category:    CategoryConfiguration,
			retryable:   false,
			disposition: DispositionFix,
		},
		{
			name:        "policy denial",
			signal:      Signal{Stage: "tool", Detail: "capability deny required for tool write_file"},
			category:    CategoryPolicy,
			retryable:   false,
			disposition: DispositionHuman,
		},
		{
			name:        "known flake",
			signal:      Signal{Stage: "verification", Evidence: "tests", Detail: "intermittent test failed", KnownFlaky: true},
			category:    CategoryFlake,
			retryable:   true,
			disposition: DispositionRetry,
		},
		{
			name:        "unknown is not auto retryable",
			signal:      Signal{Stage: "execution", Detail: "something novel happened"},
			category:    CategoryUnknown,
			retryable:   false,
			disposition: DispositionInvestigate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.signal)
			if got.Category != tt.category || got.Retryable != tt.retryable || got.Disposition != tt.disposition {
				t.Fatalf("classification = %+v, want category=%q retryable=%v disposition=%q",
					got, tt.category, tt.retryable, tt.disposition)
			}
		})
	}
}

func TestTaxonomyContainsStableCategories(t *testing.T) {
	want := []Category{
		CategoryCode,
		CategoryTest,
		CategoryEnvironment,
		CategoryInfrastructure,
		CategoryDependency,
		CategoryFlake,
		CategoryTimeout,
		CategoryResource,
		CategoryConfiguration,
		CategoryPolicy,
		CategoryUnknown,
	}
	got := Categories()
	if len(got) != len(want) {
		t.Fatalf("categories = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("categories[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDecideAutoRetryAllowsBoundedRetryDisposition(t *testing.T) {
	classification := Classification{
		Taxonomy:    TaxonomyVersion,
		Category:    CategoryInfrastructure,
		Retryable:   true,
		Disposition: DispositionRetry,
	}
	decision := DecideAutoRetry(classification, 1, 2)
	if !decision.Retry {
		t.Fatalf("Retry = false, want true: %+v", decision)
	}
	if decision.NextAttempt != 2 {
		t.Fatalf("NextAttempt = %d, want 2", decision.NextAttempt)
	}
	if decision.Reason != "retryable" {
		t.Fatalf("Reason = %q, want retryable", decision.Reason)
	}
}

func TestDecideAutoRetryStopsAtBudget(t *testing.T) {
	classification := Classification{
		Taxonomy:    TaxonomyVersion,
		Category:    CategoryInfrastructure,
		Retryable:   true,
		Disposition: DispositionRetry,
	}
	decision := DecideAutoRetry(classification, 2, 2)
	if decision.Retry {
		t.Fatalf("Retry = true, want false: %+v", decision)
	}
	if decision.Reason != "retry-budget-exhausted" {
		t.Fatalf("Reason = %q, want retry-budget-exhausted", decision.Reason)
	}
}

func TestDecideAutoRetryRequiresSameEnvironmentRetryDisposition(t *testing.T) {
	for _, classification := range []Classification{
		{Taxonomy: TaxonomyVersion, Category: CategoryResource, Retryable: true, Disposition: DispositionRetryFreshEnvironment},
		{Taxonomy: TaxonomyVersion, Category: CategoryTest, Retryable: false, Disposition: DispositionFix},
		{Taxonomy: TaxonomyVersion, Category: CategoryUnknown, Retryable: false, Disposition: DispositionInvestigate},
	} {
		decision := DecideAutoRetry(classification, 0, 2)
		if decision.Retry {
			t.Fatalf("classification %+v unexpectedly auto-retried", classification)
		}
	}
}
