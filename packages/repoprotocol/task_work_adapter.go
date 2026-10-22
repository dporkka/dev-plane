package repoprotocol

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// TaskWorkInput is the explicit boundary between an approved application task
// and durable repository work. ParallelCost is scheduler capacity, not
// TaskSpec.EstimatedCost (which represents model/monetary spending).
// RequiredGates must come from trusted repository policy, not agent output.
type TaskWorkInput struct {
	TaskID             string
	Repository         string
	Objective          string
	AcceptanceCriteria []string
	FilesToChange      []string
	FilesToCreate      []string
	Risk               RiskLevel
	ParallelCost       int
	RequiredGates      []string
	BaseSHA            string
	DependsOn          []string
}

// BuildWorkItem creates the implementation-stage work item without guessing
// ownership, risk, cost or verification policy. Failure leaves no partial item.
func BuildWorkItem(input TaskWorkInput) (WorkItem, error) {
	id := strings.TrimSpace(input.TaskID)
	if id == "" {
		return WorkItem{}, errors.New("task id is required")
	}
	if input.ParallelCost <= 0 {
		return WorkItem{}, errors.New("parallel scheduler cost must be positive and explicitly supplied")
	}
	base := strings.TrimSpace(input.BaseSHA)
	if len(base) != 40 && len(base) != 64 {
		return WorkItem{}, errors.New("task base_sha must be a complete 40- or 64-character Git object id")
	}
	if _, err := hex.DecodeString(base); err != nil {
		return WorkItem{}, fmt.Errorf("task base_sha is not hexadecimal: %w", err)
	}

	acceptance := make([]string, 0, len(input.AcceptanceCriteria))
	for _, raw := range input.AcceptanceCriteria {
		criterion := strings.TrimSpace(raw)
		if criterion == "" {
			return WorkItem{}, errors.New("empty acceptance criterion is not allowed")
		}
		acceptance = append(acceptance, criterion)
	}
	if len(acceptance) == 0 {
		return WorkItem{}, errors.New("task requires explicit acceptance criteria")
	}

	paths := make([]string, 0, len(input.FilesToChange)+len(input.FilesToCreate))
	seenPaths := make(map[string]bool)
	for _, raw := range append(append([]string(nil), input.FilesToChange...), input.FilesToCreate...) {
		normalized, err := normalizePattern(raw)
		if err != nil {
			return WorkItem{}, fmt.Errorf("invalid ownership path %q: %w", raw, err)
		}
		if !seenPaths[normalized] {
			seenPaths[normalized] = true
			paths = append(paths, normalized)
		}
	}
	if len(paths) == 0 {
		return WorkItem{}, errors.New("task requires explicit file ownership paths")
	}

	gates := make([]string, 0, len(input.RequiredGates))
	seenGates := make(map[string]bool)
	hasExecutableGate := false
	for _, raw := range input.RequiredGates {
		gate := strings.TrimSpace(raw)
		if gate == "" {
			return WorkItem{}, errors.New("empty verification gate is not allowed")
		}
		if gate != GateIndependentReview && gate != GateHumanApproval {
			hasExecutableGate = true
		}
		if !seenGates[gate] {
			seenGates[gate] = true
			gates = append(gates, gate)
		}
	}
	if !hasExecutableGate {
		return WorkItem{}, errors.New("task requires at least one executable verification gate")
	}

	dependencies := make([]string, 0, len(input.DependsOn))
	seenDeps := make(map[string]bool)
	for _, raw := range input.DependsOn {
		dependency := strings.TrimSpace(raw)
		if dependency == "" || dependency == id {
			return WorkItem{}, errors.New("task dependency must be nonempty and different from its own id")
		}
		if !seenDeps[dependency] {
			seenDeps[dependency] = true
			dependencies = append(dependencies, dependency)
		}
	}

	item := WorkItem{
		ID:                 id,
		Repository:         strings.TrimSpace(input.Repository),
		Objective:          strings.TrimSpace(input.Objective),
		AcceptanceCriteria: acceptance,
		DependsOn:          dependencies,
		OwnershipPaths:     paths,
		Risk:               input.Risk,
		Cost:               input.ParallelCost,
		RequiredGates:      gates,
		BaseSHA:            strings.ToLower(base),
		State:              WorkImplementing,
	}
	if err := item.Validate(); err != nil {
		return WorkItem{}, fmt.Errorf("invalid approved task work item: %w", err)
	}
	return item, nil
}
