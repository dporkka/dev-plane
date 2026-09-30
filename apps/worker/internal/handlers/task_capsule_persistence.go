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

type taskCapsuleStore interface {
	UpsertTaskCapsule(context.Context, dbpkg.TaskCapsuleRecord, []dbpkg.TaskLeaseRecord) error
}

func persistTaskCapsule(ctx context.Context, store taskCapsuleStore, agentRunID string, capsule scheduler.TaskCapsule) error {
	agentRunID = strings.TrimSpace(agentRunID)
	if agentRunID == "" {
		return errors.New("agent run id is required")
	}
	if store == nil {
		return errors.New("task capsule store is required")
	}

	payload, err := json.Marshal(capsule)
	if err != nil {
		return fmt.Errorf("marshal task capsule: %w", err)
	}

	leases := make([]dbpkg.TaskLeaseRecord, 0, len(capsule.Leases))
	for _, lease := range capsule.Leases {
		leases = append(leases, dbpkg.TaskLeaseRecord{
			Path: lease.Path,
			Mode: string(lease.Mode),
		})
	}

	if err := store.UpsertTaskCapsule(ctx, dbpkg.TaskCapsuleRecord{
		AgentRunID: agentRunID,
		TaskID: capsule.TaskID,
		WorkspaceID: capsule.WorkspaceID,
		Version: capsule.Version,
		AgentID: capsule.Agent.ID,
		AgentRole: capsule.Agent.Role,
		Payload: payload,
	}, leases); err != nil {
		return fmt.Errorf("persist task capsule: %w", err)
	}
	return nil
}
