package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GoalStatus represents the operational lifecycle of a durable goal.
type GoalStatus string

const (
	GoalStatusDraft     GoalStatus = "draft"
	GoalStatusActive    GoalStatus = "active"
	GoalStatusBlocked   GoalStatus = "blocked"
	GoalStatusVerifying GoalStatus = "verifying"
	GoalStatusCompleted GoalStatus = "completed"
	GoalStatusFailed    GoalStatus = "failed"
	GoalStatusCancelled GoalStatus = "cancelled"
)

// GoalCriterion is a user-visible success condition for a goal. Required
// criteria must have fresh passing proof before the goal can be proven complete.
type GoalCriterion struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// Digest binds evidence to the exact current wording and requiredness of a
// criterion. Editing a criterion therefore makes evidence for the old contract
// stale instead of silently carrying it forward.
func (c GoalCriterion) Digest() string {
	canonical := strings.TrimSpace(c.ID) + "\x00" + strings.TrimSpace(c.Description) + "\x00" + fmt.Sprintf("%t", c.Required)
	sum := sha256.Sum256([]byte(canonical))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Goal represents an outcome that can span multiple tasks and repositories.
// Tasks remain the execution primitive; Goal is the proof-bearing outcome above
// them.
type Goal struct {
	ID              string          `json:"id"`
	OrganizationID  string          `json:"organization_id"`
	CreatedBy       string          `json:"created_by"`
	Title           string          `json:"title"`
	Objective       string          `json:"objective"`
	Status          GoalStatus      `json:"status"`
	SuccessCriteria []GoalCriterion `json:"success_criteria"`
	MaxCost         *float64        `json:"max_cost,omitempty"`
	Deadline        *time.Time      `json:"deadline,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	StartedAt       *time.Time      `json:"started_at,omitempty"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// Validate checks the durable goal contract. A goal without at least one
// required success criterion is not autonomously completable and is rejected.
func (g *Goal) Validate() error {
	if g == nil {
		return errors.New("goal is required")
	}
	if strings.TrimSpace(g.OrganizationID) == "" {
		return errors.New("goal organization_id is required")
	}
	if strings.TrimSpace(g.CreatedBy) == "" {
		return errors.New("goal created_by is required")
	}
	if strings.TrimSpace(g.Title) == "" {
		return errors.New("goal title is required")
	}
	if strings.TrimSpace(g.Objective) == "" {
		return errors.New("goal objective is required")
	}
	if !validGoalStatus(g.Status) {
		return fmt.Errorf("goal has invalid status %q", g.Status)
	}
	if len(g.SuccessCriteria) == 0 {
		return errors.New("goal requires at least one success criterion")
	}

	seen := make(map[string]struct{}, len(g.SuccessCriteria))
	required := 0
	for i, criterion := range g.SuccessCriteria {
		id := strings.TrimSpace(criterion.ID)
		if id == "" {
			return fmt.Errorf("goal success criterion %d id is required", i)
		}
		if strings.TrimSpace(criterion.Description) == "" {
			return fmt.Errorf("goal success criterion %q description is required", id)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("goal success criterion id %q is duplicated", id)
		}
		seen[id] = struct{}{}
		if criterion.Required {
			required++
		}
	}
	if required == 0 {
		return errors.New("goal requires at least one required success criterion")
	}
	if g.MaxCost != nil && *g.MaxCost < 0 {
		return errors.New("goal max_cost cannot be negative")
	}
	return nil
}

func validGoalStatus(status GoalStatus) bool {
	switch status {
	case GoalStatusDraft, GoalStatusActive, GoalStatusBlocked, GoalStatusVerifying, GoalStatusCompleted, GoalStatusFailed, GoalStatusCancelled:
		return true
	default:
		return false
	}
}

// GoalProofStatus is the terminal outcome of one piece of immutable evidence.
type GoalProofStatus string

const (
	GoalProofPassed GoalProofStatus = "passed"
	GoalProofFailed GoalProofStatus = "failed"
)

// GoalProof references evidence produced elsewhere (for example a Dev Plane
// EvidenceBundle, browser verification artifact, review, deployment, or manual
// approval) without copying that evidence into the goal model.
type GoalProof struct {
	CriterionID     string          `json:"criterion_id"`
	CriterionDigest string          `json:"criterion_digest"`
	EvidenceID      string          `json:"evidence_id"`
	SubjectRevision string          `json:"subject_revision"`
	Status          GoalProofStatus `json:"status"`
	ObservedAt      time.Time       `json:"observed_at"`
}

// GoalEvaluationStatus is derived from current criteria plus their freshest
// matching evidence. It is intentionally separate from GoalStatus.
type GoalEvaluationStatus string

const (
	GoalEvaluationUnproven     GoalEvaluationStatus = "unproven"
	GoalEvaluationProven       GoalEvaluationStatus = "proven"
	GoalEvaluationContradicted GoalEvaluationStatus = "contradicted"
)

// GoalEvaluation is an explainable completion decision.
type GoalEvaluation struct {
	Status          GoalEvaluationStatus `json:"status"`
	PassedRequired  []string             `json:"passed_required,omitempty"`
	MissingRequired []string             `json:"missing_required,omitempty"`
	FailedRequired  []string             `json:"failed_required,omitempty"`
	StaleEvidence   []string             `json:"stale_evidence,omitempty"`
}

type criterionProofState struct {
	observedAt time.Time
	passed     bool
	failed     bool
}

// EvaluateGoalProof fails closed: every required criterion must have fresh
// passing evidence for its current criterion digest. Newer failures override
// older passes, and conflicting evidence at the same newest observation time is
// treated as failure. Evidence for edited criteria is reported as stale.
func EvaluateGoalProof(goal Goal, proofs []GoalProof) GoalEvaluation {
	evaluation := GoalEvaluation{Status: GoalEvaluationUnproven}

	criteria := make(map[string]GoalCriterion, len(goal.SuccessCriteria))
	for _, criterion := range goal.SuccessCriteria {
		criteria[strings.TrimSpace(criterion.ID)] = criterion
	}

	latest := make(map[string]criterionProofState, len(goal.SuccessCriteria))
	for _, proof := range proofs {
		criterion, exists := criteria[strings.TrimSpace(proof.CriterionID)]
		if !exists || proof.CriterionDigest != criterion.Digest() {
			if strings.TrimSpace(proof.EvidenceID) != "" {
				evaluation.StaleEvidence = append(evaluation.StaleEvidence, proof.EvidenceID)
			}
			continue
		}
		if strings.TrimSpace(proof.EvidenceID) == "" || strings.TrimSpace(proof.SubjectRevision) == "" || proof.ObservedAt.IsZero() {
			continue
		}
		if proof.Status != GoalProofPassed && proof.Status != GoalProofFailed {
			continue
		}

		state, ok := latest[criterion.ID]
		switch {
		case !ok || proof.ObservedAt.After(state.observedAt):
			state = criterionProofState{observedAt: proof.ObservedAt}
			if proof.Status == GoalProofPassed {
				state.passed = true
			} else {
				state.failed = true
			}
			latest[criterion.ID] = state
		case proof.ObservedAt.Equal(state.observedAt):
			if proof.Status == GoalProofPassed {
				state.passed = true
			} else {
				state.failed = true
			}
			latest[criterion.ID] = state
		}
	}

	for _, criterion := range goal.SuccessCriteria {
		if !criterion.Required {
			continue
		}
		state, exists := latest[criterion.ID]
		if !exists {
			evaluation.MissingRequired = append(evaluation.MissingRequired, criterion.ID)
			continue
		}
		if state.failed {
			evaluation.FailedRequired = append(evaluation.FailedRequired, criterion.ID)
			continue
		}
		if state.passed {
			evaluation.PassedRequired = append(evaluation.PassedRequired, criterion.ID)
			continue
		}
		evaluation.MissingRequired = append(evaluation.MissingRequired, criterion.ID)
	}

	switch {
	case len(evaluation.FailedRequired) > 0:
		evaluation.Status = GoalEvaluationContradicted
	case len(evaluation.MissingRequired) > 0:
		evaluation.Status = GoalEvaluationUnproven
	default:
		evaluation.Status = GoalEvaluationProven
	}
	return evaluation
}
