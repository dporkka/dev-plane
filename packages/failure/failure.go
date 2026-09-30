package failure

import "strings"

const TaxonomyVersion = "run-failure-v1"

type Category string

const (
	CategoryCode           Category = "code"
	CategoryTest           Category = "test"
	CategoryEnvironment    Category = "environment"
	CategoryInfrastructure Category = "infrastructure"
	CategoryDependency     Category = "dependency"
	CategoryFlake          Category = "flake"
	CategoryTimeout        Category = "timeout"
	CategoryResource       Category = "resource"
	CategoryConfiguration  Category = "configuration"
	CategoryPolicy         Category = "policy"
	CategoryUnknown        Category = "unknown"
)

type Disposition string

const (
	DispositionFix                   Disposition = "fix"
	DispositionRetry                 Disposition = "retry"
	DispositionRetryFreshEnvironment Disposition = "retry_fresh_environment"
	DispositionHuman                 Disposition = "human"
	DispositionInvestigate           Disposition = "investigate"
)

type Signal struct {
	Stage      string
	Evidence   string
	Detail     string
	KnownFlaky bool
}

type Classification struct {
	Taxonomy    string      `json:"taxonomy"`
	Category    Category    `json:"category"`
	Retryable   bool        `json:"retryable"`
	Disposition Disposition `json:"disposition"`
	Stage       string      `json:"stage,omitempty"`
	Source      string      `json:"source,omitempty"`
}

func Categories() []Category {
	return []Category{
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
}

func Classify(signal Signal) Classification {
	stage := strings.TrimSpace(signal.Stage)
	source := strings.TrimSpace(signal.Evidence)
	detail := strings.ToLower(strings.TrimSpace(signal.Detail))

	if signal.KnownFlaky {
		return classification(CategoryFlake, true, DispositionRetry, stage, source)
	}
	if containsAny(detail,
		"context deadline exceeded",
		"deadline exceeded",
		"timed out",
		"timeout exceeded",
	) {
		return classification(CategoryTimeout, true, DispositionRetry, stage, source)
	}
	if containsAny(detail,
		"out of memory",
		"cannot allocate memory",
		"no space left on device",
		"resource temporarily unavailable",
		"too many open files",
	) {
		return classification(CategoryResource, true, DispositionRetryFreshEnvironment, stage, source)
	}
	if containsAny(detail,
		"executable file not found",
		"command not found",
		"toolchain not found",
		"runtime session id is missing",
		"runtime not accessible",
	) {
		return classification(CategoryEnvironment, true, DispositionRetryFreshEnvironment, stage, source)
	}
	if containsAny(detail,
		"connection reset by peer",
		"connection refused",
		"network is unreachable",
		"temporary failure in name resolution",
		"tls handshake timeout",
		"bad gateway",
		"service unavailable",
		"gateway timeout",
	) {
		return classification(CategoryInfrastructure, true, DispositionRetry, stage, source)
	}
	if containsAny(detail,
		"no matching version found",
		"dependency resolution failed",
		"could not resolve dependency",
		"module not found",
		"package not found",
		"lock file needs to be updated",
		"lockfile needs to be updated",
	) {
		return classification(CategoryDependency, false, DispositionFix, stage, source)
	}
	if containsAny(detail,
		"capability deny",
		"denied by policy",
		"policy denied",
		"approval required",
	) {
		return classification(CategoryPolicy, false, DispositionHuman, stage, source)
	}
	if containsAny(detail,
		"missing project config",
		"missing configuration",
		"configuration is required",
		"not configured",
		"invalid configuration",
	) {
		return classification(CategoryConfiguration, false, DispositionFix, stage, source)
	}

	switch strings.ToLower(source) {
	case "tests":
		return classification(CategoryTest, false, DispositionFix, stage, source)
	case "lint", "typecheck", "build":
		return classification(CategoryCode, false, DispositionFix, stage, source)
	default:
		return classification(CategoryUnknown, false, DispositionInvestigate, stage, source)
	}
}

func classification(category Category, retryable bool, disposition Disposition, stage, source string) Classification {
	return Classification{
		Taxonomy:    TaxonomyVersion,
		Category:    category,
		Retryable:   retryable,
		Disposition: disposition,
		Stage:       stage,
		Source:      source,
	}
}

func containsAny(value string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(value, pattern) {
			return true
		}
	}
	return false
}


type AutoRetryDecision struct {
	Retry       bool   `json:"retry"`
	NextAttempt int    `json:"next_attempt,omitempty"`
	Reason      string `json:"reason"`
}

// DecideAutoRetry allows automatic retries only for failures explicitly marked
// retryable with the same-environment retry disposition. Attempts are the
// number of automatic retries already performed, not total executions.
func DecideAutoRetry(classification Classification, attempts, maxAttempts int) AutoRetryDecision {
	if !classification.Retryable {
		return AutoRetryDecision{Reason: "not-retryable"}
	}
	if classification.Disposition != DispositionRetry {
		return AutoRetryDecision{Reason: "manual-recovery-required"}
	}
	if maxAttempts <= 0 || attempts >= maxAttempts {
		return AutoRetryDecision{Reason: "retry-budget-exhausted"}
	}
	return AutoRetryDecision{
		Retry:       true,
		NextAttempt: attempts + 1,
		Reason:      "retryable",
	}
}
