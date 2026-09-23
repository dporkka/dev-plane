package models

import (
	"encoding/json"
	"errors"
	"math"
	"time"
)

const NLAPVersion = "1.0.0"

// NLAPGoal is the wire-compatible goal contract owned by the Nulang Agent
// Protocol. Dev Plane stores the canonical identifiers in task metadata rather
// than creating a parallel goal persistence model.
type NLAPGoal struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"project_id"`
	ConversationID  *string         `json:"conversation_id,omitempty"`
	Intent          string          `json:"intent"`
	DesiredState    json.RawMessage `json:"desired_state,omitempty"`
	Constraints     json.RawMessage `json:"constraints,omitempty"`
	SuccessCriteria []string        `json:"success_criteria,omitempty"`
	BudgetUSD       float64         `json:"budget_usd,omitempty"`
	Deadline        *time.Time      `json:"deadline,omitempty"`
	Status          string          `json:"status"`
}

// NLAPTask is the wire-compatible task contract. TimeoutSecs matches the
// canonical Rust serializer; UnmarshalJSON also accepts the early schema's
// legacy "timeout" field for compatibility.
type NLAPTask struct {
	ID                   string   `json:"id"`
	GoalID               string   `json:"goal_id"`
	ParentTaskID         *string  `json:"parent_task_id,omitempty"`
	Manager              string   `json:"manager"`
	Description          string   `json:"description"`
	Dependencies         []string `json:"dependencies,omitempty"`
	RequiredCapabilities []string `json:"required_capabilities,omitempty"`
	AcceptanceCriteria   []string `json:"acceptance_criteria,omitempty"`
	BudgetUSD            float64  `json:"budget_usd,omitempty"`
	TimeoutSecs          uint64   `json:"timeout_secs,omitempty"`
	Status               string   `json:"status"`
	AssignedAgentID      *string  `json:"assigned_agent_id,omitempty"`
}

func (t *NLAPTask) UnmarshalJSON(data []byte) error {
	type alias NLAPTask
	var wire struct {
		alias
		LegacyTimeout *uint64 `json:"timeout"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*t = NLAPTask(wire.alias)
	if t.TimeoutSecs == 0 && wire.LegacyTimeout != nil {
		t.TimeoutSecs = *wire.LegacyTimeout
	}
	return nil
}

type NLAPBudget struct {
	MaxCostUSD        *float64 `json:"max_cost_usd,omitempty"`
	MaxDurationSecs   *uint64  `json:"max_duration_secs,omitempty"`
	MaxModelCalls     *uint32  `json:"max_model_calls,omitempty"`
	MaxToolCalls      *uint32  `json:"max_tool_calls,omitempty"`
	MaxParallelAgents *uint32  `json:"max_parallel_agents,omitempty"`
}

// NLAPExecutionRequest is accepted by Dev Plane's NLAP ingest endpoint.
// RepositoryID is a Dev Plane binding; the nested goal/task remain canonical
// NLAP values.
type NLAPExecutionRequest struct {
	RepositoryID string      `json:"repository_id"`
	TargetBranch string      `json:"target_branch,omitempty"`
	Goal         NLAPGoal    `json:"goal"`
	Task         NLAPTask    `json:"task"`
	Budget       *NLAPBudget `json:"budget,omitempty"`
}

func (r *NLAPExecutionRequest) Validate() error {
	if r.RepositoryID == "" {
		return errors.New("repository_id is required")
	}
	if r.Goal.ID == "" {
		return errors.New("goal.id is required")
	}
	if r.Task.ID == "" {
		return errors.New("task.id is required")
	}
	if r.Task.GoalID == "" || r.Task.GoalID != r.Goal.ID {
		return errors.New("task.goal_id must match goal.id")
	}
	if r.Task.Description == "" {
		return errors.New("task.description is required")
	}
	return nil
}

// DevPlaneFields converts NLAP execution metadata into the existing Dev Plane
// task shape while preserving the protocol-owned values in spec/metadata.
func (r *NLAPExecutionRequest) DevPlaneFields() (spec, metadata, acceptance json.RawMessage, maxCost *float64, maxRuntimeMinutes int, err error) {
	spec, err = json.Marshal(map[string]any{
		"nlap_version":          NLAPVersion,
		"goal_intent":           r.Goal.Intent,
		"desired_state":         rawJSONValue(r.Goal.DesiredState),
		"constraints":           rawJSONValue(r.Goal.Constraints),
		"manager":               r.Task.Manager,
		"dependencies":          r.Task.Dependencies,
		"required_capabilities": r.Task.RequiredCapabilities,
		"parent_task_id":        r.Task.ParentTaskID,
		"assigned_agent_id":     r.Task.AssignedAgentID,
	})
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}

	metadata, err = json.Marshal(map[string]any{
		"nlap": map[string]any{
			"version":         NLAPVersion,
			"goal_id":         r.Goal.ID,
			"task_id":         r.Task.ID,
			"project_id":      r.Goal.ProjectID,
			"conversation_id": r.Goal.ConversationID,
			"goal_status":     r.Goal.Status,
			"task_status":     r.Task.Status,
		},
	})
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}

	acceptance, err = json.Marshal(r.Task.AcceptanceCriteria)
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}

	cost := r.Task.BudgetUSD
	if r.Budget != nil && r.Budget.MaxCostUSD != nil {
		cost = *r.Budget.MaxCostUSD
	}
	if cost <= 0 && r.Goal.BudgetUSD > 0 {
		cost = r.Goal.BudgetUSD
	}
	if cost > 0 {
		maxCost = &cost
	}

	timeout := r.Task.TimeoutSecs
	if r.Budget != nil && r.Budget.MaxDurationSecs != nil {
		timeout = *r.Budget.MaxDurationSecs
	}
	if timeout == 0 {
		timeout = 3600
	}
	maxRuntimeMinutes = int(math.Ceil(float64(timeout) / 60.0))
	if maxRuntimeMinutes < 1 {
		maxRuntimeMinutes = 1
	}
	return spec, metadata, acceptance, maxCost, maxRuntimeMinutes, nil
}

func rawJSONValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}
