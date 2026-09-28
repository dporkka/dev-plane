package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/scheduler"
)

type SchedulerCapacity struct {
	MaxParallel int
	CPU         float64
	MemoryMB    int
}

type SchedulerAdmission struct {
	db       *sql.DB
	capacity SchedulerCapacity
}

const schedulerAdmissionClaimTTL = 10 * time.Minute

type schedulerTaskMetadata struct {
	Scheduler *struct {
		Owns      []string `json:"owns"`
		DependsOn []string `json:"depends_on"`
		CPU       float64  `json:"cpu"`
		MemoryMB  int      `json:"memory_mb"`
	} `json:"scheduler"`
}

type schedulerTaskRow struct {
	ID           string
	ProjectID    string
	RepositoryID string
	Status       string
	Metadata     string
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

func (a *SchedulerAdmission) AdmitRun(ctx context.Context, runID, taskID string) (RunAdmissionDecision, error) {
	if a == nil || a.db == nil {
		return RunAdmissionDecision{Allowed: true}, nil
	}

	candidate, config, err := a.loadCandidate(ctx, runID, taskID)
	if err != nil {
		return RunAdmissionDecision{}, err
	}
	if config == nil {
		return RunAdmissionDecision{Allowed: true}, nil
	}

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

func (a *SchedulerAdmission) loadCandidate(ctx context.Context, runID, taskID string) (schedulerTaskRow, *struct {
	Owns      []string `json:"owns"`
	DependsOn []string `json:"depends_on"`
	CPU       float64  `json:"cpu"`
	MemoryMB  int      `json:"memory_mb"`
}, error) {
	var row schedulerTaskRow
	var runStatus string
	err := a.db.QueryRowContext(ctx, `
		SELECT t.id, t.project_id, t.repository_id, t.status, COALESCE(t.metadata, '{}'), ar.status
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		WHERE ar.id = $1 AND t.id = $2 AND t.deleted_at IS NULL
	`, runID, taskID).Scan(&row.ID, &row.ProjectID, &row.RepositoryID, &row.Status, &row.Metadata, &runStatus)
	if err != nil {
		return row, nil, fmt.Errorf("load scheduler candidate: %w", err)
	}
	if runStatus != "queued" && runStatus != "admitting" {
		return row, nil, fmt.Errorf("scheduler candidate run %s has status %s, want queued or admitting", runID, runStatus)
	}
	var metadata schedulerTaskMetadata
	if err := json.Unmarshal([]byte(row.Metadata), &metadata); err != nil {
		return row, nil, fmt.Errorf("decode scheduler metadata for task %s: %w", taskID, err)
	}
	if metadata.Scheduler == nil {
		return row, nil, nil
	}
	if len(metadata.Scheduler.Owns) == 0 {
		return row, nil, fmt.Errorf("scheduler metadata for task %s requires owns", taskID)
	}
	if metadata.Scheduler.CPU <= 0 {
		metadata.Scheduler.CPU = 1
	}
	if metadata.Scheduler.MemoryMB <= 0 {
		metadata.Scheduler.MemoryMB = 1024
	}
	return row, metadata.Scheduler, nil
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
	defer rows.Close()

	var claims []scheduler.Task
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, false, fmt.Errorf("scan running scheduler claim: %w", err)
		}
		var metadata schedulerTaskMetadata
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
			return nil, false, fmt.Errorf("decode scheduler metadata for running task %s: %w", id, err)
		}
		if metadata.Scheduler == nil || len(metadata.Scheduler.Owns) == 0 {
			return nil, true, nil
		}
		cpu := metadata.Scheduler.CPU
		if cpu <= 0 {
			cpu = 1
		}
		memoryMB := metadata.Scheduler.MemoryMB
		if memoryMB <= 0 {
			memoryMB = 1024
		}
		claims = append(claims, scheduler.Task{
			ID:   id,
			Owns: metadata.Scheduler.Owns,
			Resources: scheduler.Resources{
				CPU:      cpu,
				MemoryMB: memoryMB,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate running scheduler claims: %w", err)
	}
	return claims, false, nil
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
