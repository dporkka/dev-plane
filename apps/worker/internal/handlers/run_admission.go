package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/scheduler"
)

type RunStartBudget interface {
	CheckRunStart(ctx context.Context, runID string) (allowed bool, reason string, err error)
}

type SchedulerAdmissionConfig struct {
	MaxParallel int
	Capacity    scheduler.Capacity
	RetryAfter  time.Duration
}

type SchedulerAdmission struct {
	db     *sql.DB
	budget RunStartBudget
	config SchedulerAdmissionConfig
}

type admissionTask struct {
	RunID        string
	TaskID       string
	ProjectID    string
	RepositoryID string
	Metadata     string
}

func NewSchedulerAdmission(db *sql.DB, budget RunStartBudget, config SchedulerAdmissionConfig) *SchedulerAdmission {
	if config.RetryAfter <= 0 {
		config.RetryAfter = 5 * time.Second
	}
	return &SchedulerAdmission{db: db, budget: budget, config: config}
}

func (a *SchedulerAdmission) CheckRunAdmission(ctx context.Context, runID, taskID string) (RunAdmissionDecision, error) {
	if a == nil || a.db == nil {
		return RunAdmissionDecision{}, fmt.Errorf("scheduler admission database is not configured")
	}
	if a.budget == nil {
		return RunAdmissionDecision{}, fmt.Errorf("scheduler admission budget checker is not configured")
	}
	allowed, reason, err := a.budget.CheckRunStart(ctx, runID)
	if err != nil {
		return RunAdmissionDecision{}, fmt.Errorf("check start budget: %w", err)
	}
	if !allowed {
		if strings.TrimSpace(reason) == "" {
			reason = "start budget denied"
		}
		return RunAdmissionDecision{Allowed: false, Reason: "budget-blocked: " + reason, RetryAfter: a.config.RetryAfter}, nil
	}

	candidate, err := a.loadCandidate(ctx, runID, taskID)
	if err != nil {
		return RunAdmissionDecision{}, err
	}
	active, err := a.loadActive(ctx, candidate.ProjectID, runID)
	if err != nil {
		return RunAdmissionDecision{}, err
	}

	tasks := make([]scheduler.Task, 0, len(active)+1)
	state := make(scheduler.State, len(active)+1)
	for _, item := range active {
		task, err := a.schedulerTask(item)
		if err != nil {
			return RunAdmissionDecision{}, err
		}
		tasks = append(tasks, task)
		state[task.ID] = scheduler.StatusRunning
	}
	current, err := a.schedulerTask(candidate)
	if err != nil {
		return RunAdmissionDecision{}, err
	}
	tasks = append(tasks, current)
	state[current.ID] = scheduler.StatusPending

	decision, err := scheduler.Next(scheduler.Manifest{
		MaxParallel: a.config.MaxParallel,
		Capacity:    a.config.Capacity,
		Tasks:       tasks,
	}, state)
	if err != nil {
		return RunAdmissionDecision{}, fmt.Errorf("scheduler admission: %w", err)
	}
	for _, ready := range decision.Ready {
		if ready == current.ID {
			return RunAdmissionDecision{Allowed: true}, nil
		}
	}
	return RunAdmissionDecision{
		Allowed:    false,
		Reason:     "scheduler-blocked: ownership, concurrency, or resource capacity unavailable",
		RetryAfter: a.config.RetryAfter,
	}, nil
}

func (a *SchedulerAdmission) loadCandidate(ctx context.Context, runID, taskID string) (admissionTask, error) {
	var item admissionTask
	query := "SELECT ar.id, t.id, t.project_id, t.repository_id, COALESCE(t.metadata, '{}') " +
		"FROM agent_runs ar JOIN tasks t ON t.id = ar.task_id WHERE ar.id = $1"
	err := a.db.QueryRowContext(ctx, query, runID).Scan(
		&item.RunID, &item.TaskID, &item.ProjectID, &item.RepositoryID, &item.Metadata,
	)
	if err != nil {
		return admissionTask{}, fmt.Errorf("load admission candidate: %w", err)
	}
	if taskID != "" && item.TaskID != taskID {
		return admissionTask{}, fmt.Errorf("run %s belongs to task %s, event referenced task %s", runID, item.TaskID, taskID)
	}
	return item, nil
}

func (a *SchedulerAdmission) loadActive(ctx context.Context, projectID, excludeRunID string) ([]admissionTask, error) {
	query := "SELECT ar.id, t.id, t.project_id, t.repository_id, COALESCE(t.metadata, '{}') " +
		"FROM agent_runs ar JOIN tasks t ON t.id = ar.task_id " +
		"WHERE t.project_id = $1 AND ar.status = 'running' AND ar.id <> $2 " +
		"ORDER BY ar.created_at ASC, ar.id ASC"
	rows, err := a.db.QueryContext(ctx, query, projectID, excludeRunID)
	if err != nil {
		return nil, fmt.Errorf("load active admission claims: %w", err)
	}
	defer rows.Close()

	var result []admissionTask
	for rows.Next() {
		var item admissionTask
		if err := rows.Scan(&item.RunID, &item.TaskID, &item.ProjectID, &item.RepositoryID, &item.Metadata); err != nil {
			return nil, fmt.Errorf("scan active admission claim: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active admission claims: %w", err)
	}
	return result, nil
}

func (a *SchedulerAdmission) schedulerTask(item admissionTask) (scheduler.Task, error) {
	var root map[string]any
	if strings.TrimSpace(item.Metadata) != "" {
		if err := json.Unmarshal([]byte(item.Metadata), &root); err != nil {
			return scheduler.Task{}, fmt.Errorf("decode scheduler metadata for task %s: %w", item.TaskID, err)
		}
	}
	var owns []string
	cpu := 1.0
	memoryMB := 1024
	if raw, ok := root["scheduler"].(map[string]any); ok {
		if values, ok := raw["owns"].([]any); ok {
			for _, value := range values {
				if ownership, ok := value.(string); ok {
					owns = append(owns, ownership)
				}
			}
		}
		if value, ok := raw["cpu"].(float64); ok && value > 0 {
			cpu = value
		}
		if value, ok := raw["memory_mb"].(float64); ok && value > 0 {
			memoryMB = int(value)
		}
	}
	if len(owns) == 0 {
		owns = []string{fmt.Sprintf("repositories/%s", item.RepositoryID)}
	} else {
		namespaced := make([]string, 0, len(owns))
		for _, ownership := range owns {
			ownership = strings.TrimSpace(strings.TrimPrefix(ownership, "./"))
			if ownership == "" {
				return scheduler.Task{}, fmt.Errorf("task %s has empty scheduler ownership", item.TaskID)
			}
			namespaced = append(namespaced, fmt.Sprintf("repositories/%s/%s", item.RepositoryID, ownership))
		}
		owns = namespaced
	}
	return scheduler.Task{
		ID:   item.RunID,
		Owns: owns,
		Resources: scheduler.Resources{
			CPU:      cpu,
			MemoryMB: memoryMB,
		},
	}, nil
}
