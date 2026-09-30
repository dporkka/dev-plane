package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/readiness"
	"github.com/ai-dev-control-plane/scheduler"
)

type SchedulerCapacity struct {
	MaxParallel int
	CPU         float64
	MemoryMB    int
}

type RunStartBudget interface {
	CheckRunStart(ctx context.Context, runID string) (allowed bool, reason string, err error)
}

type SchedulerAdmission struct {
	db       *sql.DB
	capacity SchedulerCapacity
	budget   RunStartBudget
}

const schedulerAdmissionClaimTTL = 10 * time.Minute

type schedulerConfig struct {
	Owns      []string `json:"owns"`
	DependsOn []string `json:"depends_on"`
	CPU       float64  `json:"cpu"`
	MemoryMB  int      `json:"memory_mb"`
}

type schedulerTaskMetadata struct {
	Scheduler *schedulerConfig `json:"scheduler"`
}

type schedulerTaskRow struct {
	ID           string
	ProjectID    string
	RepositoryID string
	Status       string
	Metadata     string
	RunMetadata  string
}

func NewSchedulerAdmission(db *sql.DB, capacity SchedulerCapacity) *SchedulerAdmission {
	if capacity.MaxParallel <= 0 {
		capacity.MaxParallel = 1
	}
	if capacity.CPU <= 0 {
		capacity.CPU = 1
	}
	if capacity.MemoryMB <= 0 {
		capacity.MemoryMB = 1024
	}
	return &SchedulerAdmission{db: db, capacity: capacity}
}

func (a *SchedulerAdmission) WithStartBudget(budget RunStartBudget) *SchedulerAdmission {
	if a != nil {
		a.budget = budget
	}
	return a
}

func (a *SchedulerAdmission) AdmitRun(ctx context.Context, runID, taskID string) (RunAdmissionDecision, error) {
	if a == nil || a.db == nil {
		return RunAdmissionDecision{Allowed: true}, nil
	}

	candidate, config, err := a.loadCandidate(ctx, runID, taskID)
	if err != nil {
		return RunAdmissionDecision{}, err
	}
	if decision, ok, err := validatePersistedAdmission(candidate.RunMetadata); err != nil {
		return RunAdmissionDecision{}, err
	} else if ok {
		return decision, nil
	}

	if config != nil {
		for _, dependency := range config.DependsOn {
			var status string
			err := a.db.QueryRowContext(ctx, `
				SELECT status FROM tasks
				WHERE id = $1 AND deleted_at IS NULL
			`, dependency).Scan(&status)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return RunAdmissionDecision{Allowed: false, Reason: "dependency-missing:" + dependency}, nil
				}
				return RunAdmissionDecision{}, fmt.Errorf("load scheduler dependency %s: %w", dependency, err)
			}
			if status != "done" {
				return RunAdmissionDecision{Allowed: false, Reason: "dependency-pending:" + dependency}, nil
			}
		}
	}

	// Claim before budget admission so concurrent candidates are visible as
	// "admitting" to project-level concurrency accounting.
	claimed, err := a.claimRun(ctx, runID)
	if err != nil {
		return RunAdmissionDecision{}, err
	}
	if !claimed {
		return RunAdmissionDecision{Allowed: false, Reason: "run-already-claimed"}, nil
	}
	keepClaim := false
	defer func() {
		if !keepClaim {
			_ = a.ReleaseRun(context.Background(), runID)
		}
	}()

	if a.budget != nil {
		allowed, reason, err := a.budget.CheckRunStart(ctx, runID)
		if err != nil {
			return RunAdmissionDecision{}, fmt.Errorf("check run-start budget: %w", err)
		}
		if !allowed {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				reason = "start budget denied"
			}
			return RunAdmissionDecision{Allowed: false, Reason: "budget-blocked: " + reason}, nil
		}
	}

	// Legacy tasks without scheduler metadata/specs still receive atomic
	// duplicate suppression and budget admission, but bypass ownership/resource
	// scheduling until they opt into scheduler ownership.
	if config == nil {
		keepClaim = true
		return RunAdmissionDecision{Allowed: true}, nil
	}

	running, legacyRunning, err := a.loadRunningClaims(ctx, candidate)
	if err != nil {
		return RunAdmissionDecision{}, err
	}
	if legacyRunning {
		return RunAdmissionDecision{Allowed: false, Reason: "legacy-running-run-without-ownership"}, nil
	}

	tasks := make([]scheduler.Task, 0, len(running)+1)
	state := make(scheduler.State, len(running)+1)
	for _, claim := range running {
		tasks = append(tasks, claim)
		state[claim.ID] = scheduler.StatusRunning
	}
	candidateTask := scheduler.Task{
		ID:   taskID,
		Owns: config.Owns,
		Resources: scheduler.Resources{
			CPU:      config.CPU,
			MemoryMB: config.MemoryMB,
		},
	}
	tasks = append(tasks, candidateTask)
	state[taskID] = scheduler.StatusPending

	manifest := scheduler.Manifest{
		MaxParallel: a.capacity.MaxParallel,
		Capacity: scheduler.Capacity{
			CPU:      a.capacity.CPU,
			MemoryMB: a.capacity.MemoryMB,
		},
		Tasks: tasks,
	}
	decision, err := scheduler.Next(manifest, state)
	if err != nil {
		return RunAdmissionDecision{}, fmt.Errorf("scheduler decision: %w", err)
	}
	for _, ready := range decision.Ready {
		if ready == taskID {
			keepClaim = true
			return RunAdmissionDecision{Allowed: true}, nil
		}
	}

	for _, runningTask := range running {
		if scheduler.OwnershipConflict(candidateTask.Owns, runningTask.Owns) {
			return RunAdmissionDecision{Allowed: false, Reason: "ownership-conflict:" + runningTask.ID}, nil
		}
	}
	if len(running) >= a.capacity.MaxParallel {
		return RunAdmissionDecision{Allowed: false, Reason: "parallel-capacity"}, nil
	}
	return RunAdmissionDecision{Allowed: false, Reason: "resource-capacity"}, nil
}

func (a *SchedulerAdmission) claimRun(ctx context.Context, runID string) (bool, error) {
	now := time.Now().UTC()
	staleBefore := now.Add(-schedulerAdmissionClaimTTL)
	result, err := a.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'admitting', updated_at = $2
		WHERE id = $1
		  AND (
			status = 'queued'
			OR (status = 'admitting' AND updated_at < $3)
		  )
	`, runID, now, staleBefore)
	if err != nil {
		return false, fmt.Errorf("claim scheduler run %s: %w", runID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check scheduler claim %s: %w", runID, err)
	}
	return rows == 1, nil
}

func (a *SchedulerAdmission) ReleaseRun(ctx context.Context, runID string) error {
	if a == nil || a.db == nil {
		return nil
	}
	_, err := a.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'queued', updated_at = $2
		WHERE id = $1 AND status = 'admitting'
	`, runID, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("release scheduler run %s: %w", runID, err)
	}
	return nil
}

func (a *SchedulerAdmission) loadCandidate(ctx context.Context, runID, taskID string) (schedulerTaskRow, *schedulerConfig, error) {
	var row schedulerTaskRow
	var runStatus string
	err := a.db.QueryRowContext(ctx, `
		SELECT t.id, t.project_id, t.repository_id, t.status,
		       COALESCE(t.metadata, '{}'), ar.status, COALESCE(ar.metadata, '{}')
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		WHERE ar.id = $1 AND t.id = $2 AND t.deleted_at IS NULL
	`, runID, taskID).Scan(&row.ID, &row.ProjectID, &row.RepositoryID, &row.Status, &row.Metadata, &runStatus, &row.RunMetadata)
	if err != nil {
		return row, nil, fmt.Errorf("load scheduler candidate: %w", err)
	}
	if runStatus != "queued" && runStatus != "admitting" {
		return row, nil, fmt.Errorf("scheduler candidate run %s has status %s, want queued or admitting", runID, runStatus)
	}
	config, err := a.resolveSchedulerConfig(ctx, taskID, row.Metadata)
	if err != nil {
		return row, nil, err
	}
	return row, config, nil
}

type persistedAdmissionMetadata struct {
	Admission *struct {
		Policy    string           `json:"policy"`
		Readiness readiness.Report `json:"readiness"`
	} `json:"admission"`
}

func validatePersistedAdmission(raw string) (RunAdmissionDecision, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return RunAdmissionDecision{}, false, nil
	}
	var metadata persistedAdmissionMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		return RunAdmissionDecision{}, false, fmt.Errorf("decode persisted admission metadata: %w", err)
	}
	if metadata.Admission == nil {
		return RunAdmissionDecision{}, false, nil
	}
	if metadata.Admission.Policy != readiness.AdmissionPolicyVersion {
		return RunAdmissionDecision{Allowed: false, Reason: "unsupported-admission-policy"}, true, nil
	}
	switch metadata.Admission.Readiness.Status {
	case readiness.StatusReady, readiness.StatusAttention:
		return RunAdmissionDecision{}, false, nil
	case readiness.StatusBlocked:
		return RunAdmissionDecision{Allowed: false, Reason: "readiness-blocked"}, true, nil
	default:
		return RunAdmissionDecision{Allowed: false, Reason: "invalid-readiness-status"}, true, nil
	}
}

func (a *SchedulerAdmission) loadRunningClaims(ctx context.Context, candidate schedulerTaskRow) ([]scheduler.Task, bool, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT t.id, COALESCE(t.metadata, '{}')
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		WHERE ar.status IN ('admitting', 'running')
		  AND t.project_id = $1
		  AND t.repository_id = $2
		  AND t.id <> $3
		  AND t.deleted_at IS NULL
		ORDER BY t.id
	`, candidate.ProjectID, candidate.RepositoryID, candidate.ID)
	if err != nil {
		return nil, false, fmt.Errorf("load running scheduler claims: %w", err)
	}

	type rawClaim struct {
		id       string
		metadata string
	}
	rawClaims := make([]rawClaim, 0)
	for rows.Next() {
		var claim rawClaim
		if err := rows.Scan(&claim.id, &claim.metadata); err != nil {
			rows.Close()
			return nil, false, fmt.Errorf("scan running scheduler claim: %w", err)
		}
		rawClaims = append(rawClaims, claim)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, fmt.Errorf("iterate running scheduler claims: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, false, fmt.Errorf("close running scheduler claims: %w", err)
	}

	claims := make([]scheduler.Task, 0, len(rawClaims))
	for _, raw := range rawClaims {
		config, err := a.resolveSchedulerConfig(ctx, raw.id, raw.metadata)
		if err != nil {
			return nil, false, err
		}
		if config == nil {
			return nil, true, nil
		}
		claims = append(claims, scheduler.Task{
			ID:   raw.id,
			Owns: config.Owns,
			Resources: scheduler.Resources{
				CPU:      config.CPU,
				MemoryMB: config.MemoryMB,
			},
		})
	}
	return claims, false, nil
}

func (a *SchedulerAdmission) resolveSchedulerConfig(ctx context.Context, taskID, rawMetadata string) (*schedulerConfig, error) {
	var metadata schedulerTaskMetadata
	if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
		return nil, fmt.Errorf("decode scheduler metadata for task %s: %w", taskID, err)
	}
	if metadata.Scheduler != nil {
		if len(metadata.Scheduler.Owns) == 0 {
			return nil, fmt.Errorf("scheduler metadata for task %s requires owns", taskID)
		}
		applySchedulerDefaults(metadata.Scheduler)
		return metadata.Scheduler, nil
	}

	owns, found, err := a.loadTaskSpecOwnership(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	config := &schedulerConfig{Owns: owns}
	applySchedulerDefaults(config)
	return config, nil
}

func (a *SchedulerAdmission) loadTaskSpecOwnership(ctx context.Context, taskID string) ([]string, bool, error) {
	var changedRaw, createdRaw string
	err := a.db.QueryRowContext(ctx, `
		SELECT COALESCE(files_to_change, '[]'), COALESCE(files_to_create, '[]')
		FROM task_specs
		WHERE task_id = $1
		LIMIT 1
	`, taskID).Scan(&changedRaw, &createdRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("load task spec ownership for %s: %w", taskID, err)
	}

	var changed, created []string
	if err := json.Unmarshal([]byte(changedRaw), &changed); err != nil {
		return nil, false, fmt.Errorf("decode files_to_change for %s: %w", taskID, err)
	}
	if err := json.Unmarshal([]byte(createdRaw), &created); err != nil {
		return nil, false, fmt.Errorf("decode files_to_create for %s: %w", taskID, err)
	}

	seen := make(map[string]struct{}, len(changed)+len(created))
	owns := make([]string, 0, len(changed)+len(created))
	for _, value := range append(changed, created...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		owns = append(owns, value)
	}
	if len(owns) == 0 {
		return nil, false, nil
	}
	return owns, true, nil
}

func applySchedulerDefaults(config *schedulerConfig) {
	if config.CPU <= 0 {
		config.CPU = 1
	}
	if config.MemoryMB <= 0 {
		config.MemoryMB = 1024
	}
}

func parseSchedulerCapacity(maxParallel int, cpu float64, memoryMB int) SchedulerCapacity {
	return SchedulerCapacity{
		MaxParallel: maxParallel,
		CPU:         cpu,
		MemoryMB:    memoryMB,
	}
}

func schedulerReason(reason string) string {
	return strings.TrimSpace(reason)
}
