package agentrunner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ai-dev-control-plane/events"
	"github.com/nats-io/nats.go"
)

func (r *Runner) deliverRunCompletion(ctx context.Context, runID string) error {
	if r.eventBus == nil {
		return nil
	}
	// JetStream deduplicates concurrent redeliveries and retries within its
	// duplicate window if publishing succeeds but clearing the event fails.
	return r.publishPendingRunCompletion(ctx, runID, func(payload []byte) error {
		publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_, err := r.eventBus.JetStream().Publish(events.AgentRunCompleted, payload,
			nats.Context(publishCtx), nats.MsgId(events.AgentRunCompleted+":"+runID))
		return err
	})
}

func (r *Runner) publishPendingRunCompletion(ctx context.Context, runID string, publish func([]byte) error) error {
	var payload string
	err := r.db.QueryRowContext(ctx, `
		SELECT pending_completion_event FROM agent_runs
		WHERE id = $1 AND status = 'completed' AND pending_completion_event IS NOT NULL
	`, runID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load pending agent run completion: %w", err)
	}
	if err := publish([]byte(payload)); err != nil {
		return fmt.Errorf("publish pending agent run completion: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs SET pending_completion_event = NULL
		WHERE id = $1 AND pending_completion_event = $2
	`, runID, payload); err != nil {
		return fmt.Errorf("clear delivered agent run completion: %w", err)
	}
	return nil
}
