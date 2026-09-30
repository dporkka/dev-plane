package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/scheduler"
)

type taskCapsuleEvidenceStore interface {
	taskCapsuleStore
	LoadTaskCapsule(context.Context, string) (dbpkg.TaskCapsuleRecord, []dbpkg.TaskLeaseRecord, error)
}

func recordTaskCapsuleEvidence(
	ctx context.Context,
	store taskCapsuleEvidenceStore,
	agentRunID string,
	evidence scheduler.Evidence,
) (scheduler.TaskCapsule, error) {
	capsule, err := loadTaskCapsuleForRun(ctx, store, agentRunID)
	if err != nil {
		return scheduler.TaskCapsule{}, err
	}

	capsuleRevision := strings.TrimSpace(capsule.SubjectRevision)
	evidenceRevision := strings.TrimSpace(evidence.SubjectRevision)
	if capsuleRevision == "" {
		if evidenceRevision != "" {
			return scheduler.TaskCapsule{}, errors.New("task capsule subject revision is not set")
		}
	} else {
		if evidenceRevision == "" {
			return scheduler.TaskCapsule{}, errors.New("evidence subject revision is required")
		}
		if evidenceRevision != capsuleRevision {
			return scheduler.TaskCapsule{}, fmt.Errorf(
				"evidence subject revision %q does not match capsule subject revision %q",
				evidenceRevision,
				capsuleRevision,
			)
		}
	}
	evidence.SubjectRevision = evidenceRevision

	capsule.Evidence = append(capsule.Evidence, evidence)
	if err := persistTaskCapsule(ctx, store, strings.TrimSpace(agentRunID), capsule); err != nil {
		return scheduler.TaskCapsule{}, fmt.Errorf("persist capsule evidence: %w", err)
	}
	return capsule, nil
}

func advanceTaskCapsuleSubjectRevision(
	ctx context.Context,
	store taskCapsuleEvidenceStore,
	agentRunID string,
	subjectRevision string,
) (scheduler.TaskCapsule, error) {
	subjectRevision = strings.TrimSpace(subjectRevision)
	if subjectRevision == "" {
		return scheduler.TaskCapsule{}, errors.New("subject revision is required")
	}

	capsule, err := loadTaskCapsuleForRun(ctx, store, agentRunID)
	if err != nil {
		return scheduler.TaskCapsule{}, err
	}
	if strings.TrimSpace(capsule.SubjectRevision) == subjectRevision {
		return capsule, nil
	}

	capsule.SubjectRevision = subjectRevision
	if err := persistTaskCapsule(ctx, store, strings.TrimSpace(agentRunID), capsule); err != nil {
		return scheduler.TaskCapsule{}, fmt.Errorf("persist capsule subject revision: %w", err)
	}
	return capsule, nil
}

func verifyTaskCapsuleCompletion(ctx context.Context, store taskCapsuleEvidenceStore, agentRunID string) error {
	capsule, err := loadTaskCapsuleForRun(ctx, store, agentRunID)
	if err != nil {
		return err
	}
	if err := scheduler.VerifyCompletion(capsule); err != nil {
		return fmt.Errorf("verify task capsule completion: %w", err)
	}
	return nil
}

func loadTaskCapsuleForRun(
	ctx context.Context,
	store taskCapsuleEvidenceStore,
	agentRunID string,
) (scheduler.TaskCapsule, error) {
	agentRunID = strings.TrimSpace(agentRunID)
	if agentRunID == "" {
		return scheduler.TaskCapsule{}, errors.New("agent run id is required")
	}
	if store == nil {
		return scheduler.TaskCapsule{}, errors.New("task capsule store is required")
	}

	record, _, err := store.LoadTaskCapsule(ctx, agentRunID)
	if err != nil {
		return scheduler.TaskCapsule{}, fmt.Errorf("load task capsule: %w", err)
	}

	var capsule scheduler.TaskCapsule
	if err := json.Unmarshal(record.Payload, &capsule); err != nil {
		return scheduler.TaskCapsule{}, fmt.Errorf("decode task capsule: %w", err)
	}
	if record.AgentRunID != agentRunID {
		return scheduler.TaskCapsule{}, fmt.Errorf(
			"task capsule run identity mismatch: stored %q, requested %q",
			record.AgentRunID,
			agentRunID,
		)
	}
	if record.TaskID != capsule.TaskID {
		return scheduler.TaskCapsule{}, fmt.Errorf(
			"task identity mismatch: record %q, capsule %q",
			record.TaskID,
			capsule.TaskID,
		)
	}
	if record.WorkspaceID != capsule.WorkspaceID {
		return scheduler.TaskCapsule{}, fmt.Errorf(
			"workspace identity mismatch: record %q, capsule %q",
			record.WorkspaceID,
			capsule.WorkspaceID,
		)
	}
	if record.Version != capsule.Version {
		return scheduler.TaskCapsule{}, fmt.Errorf(
			"capsule version mismatch: record %d, capsule %d",
			record.Version,
			capsule.Version,
		)
	}
	if record.AgentID != capsule.Agent.ID || record.AgentRole != capsule.Agent.Role {
		return scheduler.TaskCapsule{}, fmt.Errorf(
			"agent identity mismatch: record %q/%q, capsule %q/%q",
			record.AgentID,
			record.AgentRole,
			capsule.Agent.ID,
			capsule.Agent.Role,
		)
	}

	return capsule, nil
}
