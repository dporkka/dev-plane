package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/readiness"
	"github.com/ai-dev-control-plane/scheduler"
)

// BuildAdmittedTaskCapsule snapshots the scheduler-relevant execution contract
// only after a run has acquired durable admission. The run ID is currently the
// stable execution identity because agent_runs does not persist a separate
// agent-instance identifier.
func (a *SchedulerAdmission) BuildAdmittedTaskCapsule(
	ctx context.Context,
	runID string,
	taskID string,
	requiredEvidence []string,
) (scheduler.TaskCapsule, error) {
	if a == nil || a.db == nil {
		return scheduler.TaskCapsule{}, errors.New("scheduler admission database is required")
	}

	runID = strings.TrimSpace(runID)
	taskID = strings.TrimSpace(taskID)
	if runID == "" {
		return scheduler.TaskCapsule{}, errors.New("run id is required")
	}
	if taskID == "" {
		return scheduler.TaskCapsule{}, errors.New("task id is required")
	}

	var (
		storedTaskID string
		repositoryID string
		title string
		rawMetadata string
		workspaceID sql.NullString
		agentRole string
		model sql.NullString
		provider sql.NullString
		runStatus string
	)
	err := a.db.QueryRowContext(ctx, `
		SELECT t.id, t.repository_id, t.title, COALESCE(t.metadata, '{}'),
		       ar.workspace_id, ar.agent_role, ar.model, ar.provider, ar.status
		FROM agent_runs ar
		JOIN tasks t ON t.id = ar.task_id
		WHERE ar.id = $1
		  AND t.id = $2
		  AND t.deleted_at IS NULL
	`, runID, taskID).Scan(
		&storedTaskID,
		&repositoryID,
		&title,
		&rawMetadata,
		&workspaceID,
		&agentRole,
		&model,
		&provider,
		&runStatus,
	)
	if err != nil {
		return scheduler.TaskCapsule{}, fmt.Errorf("load admitted run capsule source: %w", err)
	}
	if storedTaskID != taskID {
		return scheduler.TaskCapsule{}, fmt.Errorf("task identity mismatch: stored %q, requested %q", storedTaskID, taskID)
	}
	if runStatus != "admitting" && runStatus != "running" {
		return scheduler.TaskCapsule{}, fmt.Errorf("run %s is not admitted: status %s", runID, runStatus)
	}
	if !workspaceID.Valid || strings.TrimSpace(workspaceID.String) == "" {
		return scheduler.TaskCapsule{}, fmt.Errorf("admitted run %s is missing workspace identity", runID)
	}

	config, err := a.resolveSchedulerConfig(ctx, taskID, rawMetadata)
	if err != nil {
		return scheduler.TaskCapsule{}, err
	}

	leases := make([]scheduler.Lease, 0)
	dependsOn := make([]string, 0)
	if config != nil {
		leases = make([]scheduler.Lease, 0, len(config.Owns))
		for _, raw := range config.Owns {
			normalized, err := scheduler.NormalizeOwnershipPath(raw)
			if err != nil {
				return scheduler.TaskCapsule{}, fmt.Errorf("normalize admitted ownership %q: %w", raw, err)
			}
			leases = append(leases, scheduler.Lease{
				Path: normalized,
				Mode: scheduler.LeaseModeExclusive,
			})
		}
		dependsOn = append(dependsOn, config.DependsOn...)
	}

	if len(requiredEvidence) == 0 {
		requiredEvidence, err = a.requiredEvidenceForRepository(ctx, repositoryID)
		if err != nil {
			return scheduler.TaskCapsule{}, err
		}
	}
	required, err := normalizeCapsuleEvidenceRequirements(requiredEvidence)
	if err != nil {
		return scheduler.TaskCapsule{}, err
	}

	return scheduler.TaskCapsule{
		Version: scheduler.TaskCapsuleVersion,
		TaskID: taskID,
		WorkspaceID: strings.TrimSpace(workspaceID.String),
		Agent: scheduler.AgentIdentity{
			ID: runID,
			Role: strings.TrimSpace(agentRole),
			Provider: strings.TrimSpace(provider.String),
			Model: strings.TrimSpace(model.String),
		},
		Objective: strings.TrimSpace(title),
		DependsOn: dependsOn,
		Leases: leases,
		RequiredEvidence: required,
	}, nil
}

func normalizeCapsuleEvidenceRequirements(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for i, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, fmt.Errorf("required evidence name is empty at index %d", i)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}


func (a *SchedulerAdmission) requiredEvidenceForRepository(ctx context.Context, repositoryID string) ([]string, error) {
	var testCommand, lintCommand, typecheckCommand, buildCommand sql.NullString
	err := a.db.QueryRowContext(ctx, `
		SELECT test_command, lint_command, typecheck_command, build_command
		FROM project_configs
		WHERE repository_id = $1
		ORDER BY updated_at DESC
		LIMIT 1
	`, strings.TrimSpace(repositoryID)).Scan(
		&testCommand,
		&lintCommand,
		&typecheckCommand,
		&buildCommand,
	)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("load repository verification commands: %w", err)
	}

	plan := readiness.BuildVerificationPlan(
		testCommand.String,
		lintCommand.String,
		typecheckCommand.String,
		buildCommand.String,
	)
	required := make([]string, 0, len(plan))
	for _, step := range plan {
		required = append(required, step.Evidence)
	}
	return required, nil
}
