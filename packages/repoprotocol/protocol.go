package repoprotocol

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

const (
	CurrentVersion = 1

	GateIndependentReview = "independent_review"
	GateHumanApproval     = "human_approval"
)

type Isolation string

const (
	IsolationNone      Isolation = "none"
	IsolationWorktree  Isolation = "worktree"
	IsolationContainer Isolation = "container"
)

type RiskLevel string

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

type WorkState string

const (
	WorkDraft        WorkState = "draft"
	WorkReady        WorkState = "ready"
	WorkClaimed      WorkState = "claimed"
	WorkImplementing WorkState = "implementing"
	WorkVerifying    WorkState = "verifying"
	WorkReviewing    WorkState = "reviewing"
	WorkReadyToLand  WorkState = "ready_to_land"
	WorkLanded       WorkState = "landed"
	WorkFailed       WorkState = "failed"
	WorkCancelled    WorkState = "cancelled"
)

type GateStatus string

const (
	GatePending GateStatus = "pending"
	GatePassed  GateStatus = "passed"
	GateFailed  GateStatus = "failed"
	GateSkipped GateStatus = "skipped"
)

type Config struct {
	Version      int                            `json:"version" yaml:"version"`
	Commands     Commands                       `json:"commands,omitempty" yaml:"commands,omitempty"`
	Verification map[string]VerificationProfile `json:"verification" yaml:"verification"`
	Work         WorkConfig                     `json:"work" yaml:"work"`
	Review       ReviewConfig                   `json:"review,omitempty" yaml:"review,omitempty"`
	Risk         []RiskRule                     `json:"risk,omitempty" yaml:"risk,omitempty"`
}

type Commands struct {
	Doctor string `json:"doctor,omitempty" yaml:"doctor,omitempty"`
	Dev    string `json:"dev,omitempty" yaml:"dev,omitempty"`
	Logs   string `json:"logs,omitempty" yaml:"logs,omitempty"`
}

type VerificationKind string

const (
	VerificationKindCommand VerificationKind = "command"
	VerificationKindBrowser VerificationKind = "browser"
)

type BrowserVerificationProfile struct {
	Command       string   `json:"command" yaml:"command"`
	ReportPath    string   `json:"report_path,omitempty" yaml:"report_path,omitempty"`
	ArtifactPaths []string `json:"artifact_paths,omitempty" yaml:"artifact_paths,omitempty"`
}

type VerificationProfile struct {
	Command        string                      `json:"command,omitempty" yaml:"command,omitempty"`
	TimeoutSeconds int                         `json:"timeout_seconds,omitempty" yaml:"timeout_seconds,omitempty"`
	Browser        *BrowserVerificationProfile `json:"browser,omitempty" yaml:"browser,omitempty"`
}

type WorkConfig struct {
	Isolation           Isolation `json:"isolation" yaml:"isolation"`
	MaxParallelCost     int       `json:"max_parallel_cost" yaml:"max_parallel_cost"`
	RequireCleanHandoff bool      `json:"require_clean_handoff,omitempty" yaml:"require_clean_handoff,omitempty"`
}

type ReviewConfig struct {
	Independent bool `json:"independent,omitempty" yaml:"independent,omitempty"`
	ExactHead   bool `json:"exact_head,omitempty" yaml:"exact_head,omitempty"`
}

type RiskRule struct {
	Paths    []string  `json:"paths" yaml:"paths"`
	Level    RiskLevel `json:"level" yaml:"level"`
	Requires []string  `json:"requires,omitempty" yaml:"requires,omitempty"`
}

type EvidenceArtifact struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type GateEvidence struct {
	Name      string             `json:"name"`
	Kind      VerificationKind   `json:"kind,omitempty"`
	Status    GateStatus         `json:"status"`
	Command   string             `json:"command,omitempty"`
	Output    string             `json:"output,omitempty"`
	Artifacts []EvidenceArtifact `json:"artifacts,omitempty"`
}

type EvidenceBundle struct {
	WorkItemID string         `json:"work_item_id,omitempty"`
	BaseSHA    string         `json:"base_sha"`
	HeadSHA    string         `json:"head_sha"`
	Gates      []GateEvidence `json:"gates"`
}

type WorkItem struct {
	ID                 string     `json:"id"`
	Repository         string     `json:"repository"`
	Objective          string     `json:"objective"`
	AcceptanceCriteria []string   `json:"acceptance_criteria,omitempty"`
	ParentID           string     `json:"parent_id,omitempty"`
	DiscoveredFromID   string     `json:"discovered_from_id,omitempty"`
	DependsOn          []string   `json:"depends_on,omitempty"`
	OwnershipPaths     []string   `json:"ownership_paths"`
	Risk               RiskLevel  `json:"risk"`
	Cost               int        `json:"cost"`
	RequiredGates      []string   `json:"required_gates,omitempty"`
	BaseSHA            string     `json:"base_sha"`
	ClaimedBy          string     `json:"claimed_by,omitempty"`
	LeaseUntil         *time.Time `json:"lease_until,omitempty"`
	State              WorkState  `json:"state"`
}

func (c Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported version %d: expected %d", c.Version, CurrentVersion)
	}
	if len(c.Verification) == 0 {
		return errors.New("verification requires at least one profile")
	}
	for name, profile := range c.Verification {
		name = strings.TrimSpace(name)
		if name == "" {
			return errors.New("verification profile name is required")
		}
		hasCommand := strings.TrimSpace(profile.Command) != ""
		hasBrowser := profile.Browser != nil
		if hasCommand == hasBrowser {
			return fmt.Errorf("verification profile %q requires exactly one of command or browser", name)
		}
		if profile.TimeoutSeconds < 0 {
			return fmt.Errorf("verification profile %q timeout_seconds cannot be negative", name)
		}
		if profile.Browser != nil {
			if strings.TrimSpace(profile.Browser.Command) == "" {
				return fmt.Errorf("verification profile %q browser requires a command", name)
			}
			if reportPath := strings.TrimSpace(profile.Browser.ReportPath); reportPath != "" {
				if _, err := normalizeRepositoryPath(reportPath); err != nil {
					return fmt.Errorf("verification profile %q browser report_path %q: %w", name, reportPath, err)
				}
			}
			for _, artifactPath := range profile.Browser.ArtifactPaths {
				if _, err := normalizeRepositoryPath(artifactPath); err != nil {
					return fmt.Errorf("verification profile %q browser artifact path %q: %w", name, artifactPath, err)
				}
			}
		}
	}

	switch c.Work.Isolation {
	case IsolationNone, IsolationWorktree, IsolationContainer:
	default:
		return fmt.Errorf("unsupported work isolation %q", c.Work.Isolation)
	}
	if c.Work.MaxParallelCost <= 0 {
		return errors.New("work max_parallel_cost must be positive")
	}

	for i, rule := range c.Risk {
		if !validRisk(rule.Level) {
			return fmt.Errorf("risk rule %d has invalid level %q", i, rule.Level)
		}
		if len(rule.Paths) == 0 {
			return fmt.Errorf("risk rule %d requires at least one path", i)
		}
		for _, raw := range rule.Paths {
			if _, err := normalizePattern(raw); err != nil {
				return fmt.Errorf("risk rule %d path %q: %w", i, raw, err)
			}
		}
		for _, gate := range rule.Requires {
			if !c.knownGate(gate) {
				return fmt.Errorf("risk rule %d requires unknown gate %q", i, gate)
			}
		}
	}
	return nil
}

func (c Config) knownGate(gate string) bool {
	gate = strings.TrimSpace(gate)
	if gate == GateIndependentReview || gate == GateHumanApproval {
		return true
	}
	_, ok := c.Verification[gate]
	return ok
}

func (c Config) RequiredGatesForPaths(changedPaths []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, rule := range c.Risk {
		if !ruleMatches(rule, changedPaths) {
			continue
		}
		for _, gate := range rule.Requires {
			if _, exists := seen[gate]; exists {
				continue
			}
			seen[gate] = struct{}{}
			result = append(result, gate)
		}
	}
	return result
}

func (e EvidenceBundle) ValidateHead(candidateHead string) error {
	candidateHead = strings.TrimSpace(candidateHead)
	if candidateHead == "" {
		return errors.New("candidate head is required")
	}
	if strings.TrimSpace(e.BaseSHA) == "" {
		return errors.New("evidence base_sha is required")
	}
	if strings.TrimSpace(e.HeadSHA) == "" {
		return errors.New("evidence head_sha is required")
	}
	if e.HeadSHA != candidateHead {
		return fmt.Errorf("evidence head mismatch: bundle=%s candidate=%s", e.HeadSHA, candidateHead)
	}
	if len(e.Gates) == 0 {
		return errors.New("evidence requires at least one gate")
	}
	for _, gate := range e.Gates {
		if strings.TrimSpace(gate.Name) == "" {
			return errors.New("evidence gate name is required")
		}
		if gate.Status != GatePassed {
			return fmt.Errorf("evidence gate %q is not passed: %s", gate.Name, gate.Status)
		}
	}
	return nil
}

func (w WorkItem) Validate() error {
	if strings.TrimSpace(w.ID) == "" {
		return errors.New("work item id is required")
	}
	if strings.TrimSpace(w.Repository) == "" {
		return errors.New("work item repository is required")
	}
	if strings.TrimSpace(w.Objective) == "" {
		return errors.New("work item objective is required")
	}
	if len(w.OwnershipPaths) == 0 {
		return errors.New("work item requires at least one ownership path")
	}
	for _, raw := range w.OwnershipPaths {
		if _, err := normalizePattern(raw); err != nil {
			return fmt.Errorf("work item ownership path %q: %w", raw, err)
		}
	}
	if !validRisk(w.Risk) {
		return fmt.Errorf("work item has invalid risk %q", w.Risk)
	}
	if w.Cost <= 0 {
		return errors.New("work item cost must be positive")
	}
	if strings.TrimSpace(w.BaseSHA) == "" {
		return errors.New("work item base_sha is required")
	}
	if !validWorkState(w.State) {
		return fmt.Errorf("work item has invalid state %q", w.State)
	}
	if w.State == WorkClaimed {
		if strings.TrimSpace(w.ClaimedBy) == "" {
			return errors.New("claimed work item requires claimed_by")
		}
		if w.LeaseUntil == nil {
			return errors.New("claimed work item requires lease_until")
		}
	}
	return nil
}

func (w WorkItem) CanClaim() bool {
	return w.State == WorkReady
}

func validRisk(level RiskLevel) bool {
	switch level {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

func validWorkState(state WorkState) bool {
	switch state {
	case WorkDraft, WorkReady, WorkClaimed, WorkImplementing, WorkVerifying, WorkReviewing, WorkReadyToLand, WorkLanded, WorkFailed, WorkCancelled:
		return true
	default:
		return false
	}
}

func ruleMatches(rule RiskRule, changedPaths []string) bool {
	for _, pattern := range rule.Paths {
		for _, changed := range changedPaths {
			if pathMatches(pattern, changed) {
				return true
			}
		}
	}
	return false
}

func pathMatches(pattern, changed string) bool {
	normalizedPattern, err := normalizePattern(pattern)
	if err != nil {
		return false
	}
	normalizedChanged, err := normalizeRepositoryPath(changed)
	if err != nil {
		return false
	}

	switch {
	case strings.HasSuffix(normalizedPattern, "/**"):
		base := strings.TrimSuffix(normalizedPattern, "/**")
		return normalizedChanged == base || strings.HasPrefix(normalizedChanged, base+"/")
	case strings.HasSuffix(normalizedPattern, "/*"):
		base := strings.TrimSuffix(normalizedPattern, "/*")
		if !strings.HasPrefix(normalizedChanged, base+"/") {
			return false
		}
		remainder := strings.TrimPrefix(normalizedChanged, base+"/")
		return remainder != "" && !strings.Contains(remainder, "/")
	default:
		return normalizedChanged == normalizedPattern
	}
}

func normalizePattern(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "./")
	suffix := ""
	switch {
	case strings.HasSuffix(value, "/**"):
		suffix = "/**"
		value = strings.TrimSuffix(value, "/**")
	case strings.HasSuffix(value, "/*"):
		suffix = "/*"
		value = strings.TrimSuffix(value, "/*")
	}
	normalized, err := normalizeRepositoryPath(value)
	if err != nil {
		return "", err
	}
	return normalized + suffix, nil
}

func normalizeRepositoryPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "./")
	value = strings.TrimSuffix(value, "/")
	if value == "" || strings.HasPrefix(value, "/") {
		return "", errors.New("path must be repository-relative")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("path must be repository-relative and stay inside the repository")
	}
	return cleaned, nil
}
