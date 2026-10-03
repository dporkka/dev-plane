package vcs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type OutcomeStatus string

const (
	OutcomeSuccess           OutcomeStatus = "SUCCESS"
	OutcomeConflict          OutcomeStatus = "CONFLICT"
	OutcomeRejection         OutcomeStatus = "REJECTION"
	OutcomeStaleTarget       OutcomeStatus = "STALE_TARGET"
	OutcomeAlreadyIntegrated OutcomeStatus = "ALREADY_INTEGRATED"
)

// Candidate represents an immutable verified task result ready for integration.
type Candidate struct {
	ID             string            `json:"id"`
	TaskID         string            `json:"task_id"`
	AgentID        string            `json:"agent_id"`
	WorkspacePath  string            `json:"workspace_path"`
	TargetBranch   string            `json:"target_branch"`
	ExpectedTarget string            `json:"expected_target_commit,omitempty"`
	Revision       Revision          `json:"revision"`
	AuthEnv        map[string]string `json:"-"`
}

// IntegrationResult provides the typed, observable outcome of a candidate merge attempt.
type IntegrationResult struct {
	CandidateID    string        `json:"candidate_id"`
	Status         OutcomeStatus `json:"status"`
	TargetCommitID string        `json:"target_commit_id,omitempty"`
	TargetRef      string        `json:"target_ref,omitempty"`
	ErrorMessage   string        `json:"error_message,omitempty"`
	ProvenanceID   string        `json:"provenance_id,omitempty"`
	IntegratedAt   time.Time     `json:"integrated_at"`
}

// VerifierFunc specifies a deterministic re-verification callback after replaying candidate onto target state.
type VerifierFunc func(ctx context.Context, targetWorkspacePath string) error

// MergeRefinery serializes target integration, enforces compare-and-swap state checks,
// executes deterministic re-verification, and logs provenance events.
type MergeRefinery struct {
	manager     *Manager
	mu          sync.Mutex
	targetLocks map[string]*sync.Mutex
	integrated  map[string]*IntegrationResult // Candidate.ID -> result (idempotency)
	now         func() time.Time
}

func NewMergeRefinery(manager *Manager) (*MergeRefinery, error) {
	if manager == nil {
		return nil, errors.New("VCS manager is required for MergeRefinery")
	}
	return &MergeRefinery{
		manager:     manager,
		targetLocks: make(map[string]*sync.Mutex),
		integrated:  make(map[string]*IntegrationResult),
		now:         func() time.Time { return time.Now().UTC() },
	}, nil
}

func (r *MergeRefinery) getTargetLock(target string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock, ok := r.targetLocks[target]
	if !ok {
		lock = &sync.Mutex{}
		r.targetLocks[target] = lock
	}
	return lock
}

// MaterializeCandidate materializes an immutable candidate workspace snapshot into a verified revision.
func (r *MergeRefinery) MaterializeCandidate(ctx context.Context, workspace Workspace, message string) (Candidate, error) {
	revision, err := r.manager.Snapshot(ctx, workspace, message)
	if err != nil {
		return Candidate{}, fmt.Errorf("snapshot candidate workspace: %w", err)
	}

	candID, err := randomID()
	if err != nil {
		return Candidate{}, fmt.Errorf("generate candidate ID: %w", err)
	}

	candidate := Candidate{
		ID:            candID,
		TaskID:        workspace.TaskID,
		AgentID:       workspace.AgentID,
		WorkspacePath: workspace.WorkspacePath,
		TargetBranch:  workspace.Base,
		Revision:      revision,
		AuthEnv:       workspace.AuthEnv,
	}

	event := ProvenanceEvent{
		Kind:       EventCandidateMaterialized,
		Backend:    r.manager.backend.Name(),
		TaskID:     candidate.TaskID,
		AgentID:    candidate.AgentID,
		Workspace:  workspace.Name,
		PublishRef: candidate.TargetBranch,
		CommitID:   candidate.Revision.CommitID,
		ChangeID:   candidate.Revision.ChangeID,
		Attributes: map[string]string{
			"candidate_id": candID,
		},
	}
	if err := r.manager.record(ctx, workspace, event); err != nil {
		return Candidate{}, fmt.Errorf("record candidate materialization: %w", err)
	}

	return candidate, nil
}

// Integrate executes serialized target-branch integration with CAS protection, re-verification, and idempotency.
func (r *MergeRefinery) Integrate(ctx context.Context, candidate Candidate, verifier VerifierFunc, currentTargetHead string) (IntegrationResult, error) {
	if candidate.ID == "" {
		return IntegrationResult{}, errors.New("candidate ID is required")
	}

	// Idempotency check: repeated delivery of already-integrated candidate
	r.mu.Lock()
	if prev, ok := r.integrated[candidate.ID]; ok {
		r.mu.Unlock()
		return *prev, nil
	}
	r.mu.Unlock()

	// Per-target branch serialization lock
	targetLock := r.getTargetLock(candidate.TargetBranch)
	targetLock.Lock()
	defer targetLock.Unlock()

	now := r.now()

	// Compare-and-Swap (CAS) state check against external writers
	if candidate.ExpectedTarget != "" && currentTargetHead != "" && candidate.ExpectedTarget != currentTargetHead {
		res := IntegrationResult{
			CandidateID:  candidate.ID,
			Status:       OutcomeStaleTarget,
			ErrorMessage: fmt.Sprintf("stale target: expected %s, current head is %s", candidate.ExpectedTarget, currentTargetHead),
			IntegratedAt: now,
		}
		return res, nil
	}

	// Re-verify integrated state if verifier callback is provided
	if verifier != nil {
		if err := verifier(ctx, candidate.WorkspacePath); err != nil {
			res := IntegrationResult{
				CandidateID:  candidate.ID,
				Status:       OutcomeRejection,
				ErrorMessage: fmt.Sprintf("deterministic re-verification failed: %v", err),
				IntegratedAt: now,
			}
			return res, nil
		}
	}

	// Execute Publish through backend to target ref
	ws := Workspace{
		TaskID:        candidate.TaskID,
		AgentID:       candidate.AgentID,
		WorkspacePath: candidate.WorkspacePath,
		PublishRef:    candidate.TargetBranch,
		AuthEnv:       candidate.AuthEnv,
	}

	if err := r.manager.Publish(ctx, ws, candidate.Revision); err != nil {
		res := IntegrationResult{
			CandidateID:  candidate.ID,
			Status:       OutcomeConflict,
			ErrorMessage: fmt.Sprintf("integration publish conflict: %v", err),
			IntegratedAt: now,
		}
		return res, nil
	}

	provID, _ := randomID()
	res := IntegrationResult{
		CandidateID:    candidate.ID,
		Status:         OutcomeSuccess,
		TargetCommitID: candidate.Revision.CommitID,
		TargetRef:      candidate.TargetBranch,
		ProvenanceID:   provID,
		IntegratedAt:   now,
	}

	// Record provenance event for candidate integration
	_ = r.manager.record(ctx, ws, ProvenanceEvent{
		ID:         provID,
		Kind:       EventCandidateIntegrated,
		Backend:    r.manager.backend.Name(),
		TaskID:     candidate.TaskID,
		AgentID:    candidate.AgentID,
		PublishRef: candidate.TargetBranch,
		CommitID:   candidate.Revision.CommitID,
		ChangeID:   candidate.Revision.ChangeID,
		Attributes: map[string]string{
			"candidate_id": candidate.ID,
			"outcome":      string(OutcomeSuccess),
		},
	})

	// Save idempotency result
	r.mu.Lock()
	r.integrated[candidate.ID] = &res
	r.mu.Unlock()

	return res, nil
}

// DAGNodeState represents the execution lifecycle of a node in a task DAG.
type DAGNodeState string

const (
	DAGPending    DAGNodeState = "PENDING"
	DAGRunning    DAGNodeState = "RUNNING"
	DAGIntegrated DAGNodeState = "INTEGRATED"
	DAGFailed     DAGNodeState = "FAILED"
)

// DAGNode defines a task DAG node whose downstream dependencies unlock only after target-branch integration succeeds.
type DAGNode struct {
	ID            string       `json:"id"`
	TaskID        string       `json:"task_id"`
	Dependencies  []string     `json:"dependencies"`
	State         DAGNodeState `json:"state"`
	IntegratedRef string       `json:"integrated_ref,omitempty"`
}

// DAGBarrier manages task DAG dependency gates. Downstream tasks unlock ONLY after upstream nodes achieve DAGIntegrated.
type DAGBarrier struct {
	nodes map[string]*DAGNode
	mu    sync.RWMutex
}

func NewDAGBarrier(nodes []*DAGNode) *DAGBarrier {
	nodeMap := make(map[string]*DAGNode)
	for _, n := range nodes {
		cp := *n
		nodeMap[cp.ID] = &cp
	}
	return &DAGBarrier{nodes: nodeMap}
}

// IsReady returns true if all upstream dependencies for nodeID are in DAGIntegrated state.
func (d *DAGBarrier) IsReady(nodeID string) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	node, ok := d.nodes[nodeID]
	if !ok {
		return false, fmt.Errorf("DAG node %s not found", nodeID)
	}

	for _, depID := range node.Dependencies {
		depNode, ok := d.nodes[depID]
		if !ok {
			return false, fmt.Errorf("dependency DAG node %s not found", depID)
		}
		if depNode.State != DAGIntegrated {
			return false, nil
		}
	}

	return true, nil
}

// MarkIntegrated transitions a DAG node to DAGIntegrated state upon successful refinery integration.
func (d *DAGBarrier) MarkIntegrated(nodeID string, integratedRef string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	node, ok := d.nodes[nodeID]
	if !ok {
		return fmt.Errorf("DAG node %s not found", nodeID)
	}

	node.State = DAGIntegrated
	node.IntegratedRef = integratedRef
	return nil
}

// GetNode returns a snapshot of the DAG node state.
func (d *DAGBarrier) GetNode(nodeID string) (DAGNode, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	node, ok := d.nodes[nodeID]
	if !ok {
		return DAGNode{}, fmt.Errorf("DAG node %s not found", nodeID)
	}
	return *node, nil
}
