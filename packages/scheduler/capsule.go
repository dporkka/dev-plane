package scheduler

import (
	"errors"
	"fmt"
	"strings"
)

const TaskCapsuleVersion = 1

type LeaseMode string

const (
	LeaseModeExclusive LeaseMode = "exclusive"
)

type EvidenceStatus string

const (
	EvidenceStatusPassed EvidenceStatus = "passed"
	EvidenceStatusFailed EvidenceStatus = "failed"
)

// AgentIdentity identifies the worker that owns a task capsule. Provider and
// model are descriptive only; the stable ID and role are the portable identity.
type AgentIdentity struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// ContextRef points at durable context without coupling the scheduler to the
// backing memory system. AgentVault, git, artifact stores, and future providers
// can all participate through stable URIs.
type ContextRef struct {
	Kind   string `json:"kind"`
	URI    string `json:"uri"`
	Digest string `json:"digest,omitempty"`
}

// Lease is the portable ownership claim derived from a scheduler task's Owns
// paths. Leases are advisory at this layer; the runtime can persist/enforce them.
type Lease struct {
	Path string    `json:"path"`
	Mode LeaseMode `json:"mode"`
}

// Evidence records a machine-checkable completion signal. Artifact may point at
// a log, report, trace, screenshot, or other persisted proof.
type Evidence struct {
	Name            string         `json:"name"`
	Kind            string         `json:"kind"`
	Status          EvidenceStatus `json:"status"`
	Command         string         `json:"command,omitempty"`
	Artifact        string         `json:"artifact,omitempty"`
	SubjectRevision string         `json:"subject_revision,omitempty"`
}

// TaskCapsule is the portable handoff contract between scheduling, execution,
// verification, and durable memory.
type TaskCapsule struct {
	Version          int           `json:"version"`
	TaskID           string        `json:"task_id"`
	WorkspaceID      string        `json:"workspace_id"`
	Agent            AgentIdentity `json:"agent"`
	Objective        string        `json:"objective,omitempty"`
	SubjectRevision  string        `json:"subject_revision,omitempty"`
	DependsOn        []string      `json:"depends_on,omitempty"`
	Leases           []Lease       `json:"leases"`
	ContextRefs      []ContextRef  `json:"context_refs,omitempty"`
	RequiredEvidence []string      `json:"required_evidence,omitempty"`
	Evidence         []Evidence    `json:"evidence,omitempty"`
}

type CapsuleOptions struct {
	Agent            AgentIdentity
	WorkspaceID      string
	Objective        string
	SubjectRevision  string
	ContextRefs      []ContextRef
	RequiredEvidence []string
}

// BuildTaskCapsule creates a capsule only when the scheduler actually admits
// taskID in the current decision. This prevents a worker from manufacturing a
// runnable capsule for blocked, conflicting, or capacity-excluded work.
func BuildTaskCapsule(manifest Manifest, state State, taskID string, options CapsuleOptions) (TaskCapsule, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return TaskCapsule{}, errors.New("task id is required")
	}
	if strings.TrimSpace(options.Agent.ID) == "" {
		return TaskCapsule{}, errors.New("agent id is required")
	}
	if strings.TrimSpace(options.Agent.Role) == "" {
		return TaskCapsule{}, errors.New("agent role is required")
	}
	if strings.TrimSpace(options.WorkspaceID) == "" {
		return TaskCapsule{}, errors.New("workspace id is required")
	}
	for i, ref := range options.ContextRefs {
		if strings.TrimSpace(ref.Kind) == "" {
			return TaskCapsule{}, fmt.Errorf("context ref kind is required at index %d", i)
		}
		if strings.TrimSpace(ref.URI) == "" {
			return TaskCapsule{}, fmt.Errorf("context ref uri is required at index %d", i)
		}
	}

	decision, err := Next(manifest, state)
	if err != nil {
		return TaskCapsule{}, err
	}
	if !containsID(decision.Ready, taskID) {
		return TaskCapsule{}, fmt.Errorf("task %s is not admitted by scheduler", taskID)
	}

	var selected Task
	found := false
	for _, task := range manifest.Tasks {
		if task.ID == taskID {
			selected = task
			found = true
			break
		}
	}
	if !found {
		return TaskCapsule{}, fmt.Errorf("task %s not found", taskID)
	}

	leases := make([]Lease, 0, len(selected.Owns))
	for _, raw := range selected.Owns {
		ownership, err := normalizeOwnership(raw)
		if err != nil {
			return TaskCapsule{}, fmt.Errorf("task %s ownership: %w", taskID, err)
		}
		leases = append(leases, Lease{
			Path: ownership,
			Mode: LeaseModeExclusive,
		})
	}

	requiredEvidence, err := normalizeEvidenceNames(options.RequiredEvidence)
	if err != nil {
		return TaskCapsule{}, err
	}

	return TaskCapsule{
		Version:          TaskCapsuleVersion,
		TaskID:           taskID,
		WorkspaceID:      strings.TrimSpace(options.WorkspaceID),
		Agent:            normalizeAgentIdentity(options.Agent),
		Objective:        strings.TrimSpace(options.Objective),
		SubjectRevision:  strings.TrimSpace(options.SubjectRevision),
		DependsOn:        append([]string(nil), selected.DependsOn...),
		Leases:           leases,
		ContextRefs:      append([]ContextRef(nil), options.ContextRefs...),
		RequiredEvidence: requiredEvidence,
	}, nil
}

// VerifyCompletion validates that every required evidence name has a latest
// result and that the latest result passed. Appending a later failure therefore
// invalidates an earlier pass instead of allowing stale proof to close work.
func VerifyCompletion(capsule TaskCapsule) error {
	if capsule.Version != TaskCapsuleVersion {
		return fmt.Errorf("unsupported task capsule version %d", capsule.Version)
	}
	if strings.TrimSpace(capsule.TaskID) == "" {
		return errors.New("task id is required")
	}
	if strings.TrimSpace(capsule.WorkspaceID) == "" {
		return errors.New("workspace id is required")
	}
	if strings.TrimSpace(capsule.Agent.ID) == "" {
		return errors.New("agent id is required")
	}
	if strings.TrimSpace(capsule.Agent.Role) == "" {
		return errors.New("agent role is required")
	}

	required, err := normalizeEvidenceNames(capsule.RequiredEvidence)
	if err != nil {
		return err
	}

	subjectRevision := strings.TrimSpace(capsule.SubjectRevision)
	latest := make(map[string]Evidence, len(capsule.Evidence))
	for i, evidence := range capsule.Evidence {
		name := strings.TrimSpace(evidence.Name)
		if name == "" {
			return fmt.Errorf("evidence name is required at index %d", i)
		}
		switch evidence.Status {
		case EvidenceStatusPassed, EvidenceStatusFailed:
		default:
			return fmt.Errorf("evidence %s has invalid status %q", name, evidence.Status)
		}

		evidence.Name = name
		evidence.SubjectRevision = strings.TrimSpace(evidence.SubjectRevision)
		if subjectRevision != "" {
			if evidence.SubjectRevision == "" {
				return fmt.Errorf("evidence %s subject revision is required", name)
			}
			if evidence.SubjectRevision != subjectRevision {
				continue
			}
		}
		latest[name] = evidence
	}

	for _, name := range required {
		evidence, ok := latest[name]
		if !ok {
			if subjectRevision != "" {
				return fmt.Errorf(
					"required evidence %s is missing for current subject revision %s",
					name,
					subjectRevision,
				)
			}
			return fmt.Errorf("required evidence %s is missing", name)
		}
		if evidence.Status != EvidenceStatusPassed {
			return fmt.Errorf("required evidence %s failed", name)
		}
	}
	return nil
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func normalizeAgentIdentity(agent AgentIdentity) AgentIdentity {
	agent.ID = strings.TrimSpace(agent.ID)
	agent.Role = strings.TrimSpace(agent.Role)
	agent.Provider = strings.TrimSpace(agent.Provider)
	agent.Model = strings.TrimSpace(agent.Model)
	return agent
}

func normalizeEvidenceNames(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for i, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("required evidence name is empty at index %d", i)
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}
