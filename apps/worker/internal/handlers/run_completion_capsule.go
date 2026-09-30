package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dbpkg "github.com/ai-dev-control-plane/db"
	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/scheduler"
)

type completedRunVerificationData struct {
	SubjectRevision string               `json:"subject_revision"`
	Evidence        []scheduler.Evidence `json:"evidence"`
}

func (h *RunHandler) verifyCompletedRunCapsule(ctx context.Context, event events.AgentRunEvent) error {
	if h == nil || h.db == nil || strings.TrimSpace(event.RunID) == "" {
		return nil
	}

	store := dbpkg.NewTaskCapsuleSQLStore(h.db)
	record, _, err := store.LoadTaskCapsule(ctx, event.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load completed run capsule: %w", err)
	}

	var capsule scheduler.TaskCapsule
	if err := json.Unmarshal(record.Payload, &capsule); err != nil {
		return fmt.Errorf("decode completed run capsule: %w", err)
	}

	if len(event.Data) == 0 || string(event.Data) == "null" || string(event.Data) == "{}" {
		if len(capsule.RequiredEvidence) == 0 {
			return nil
		}
		return errors.New("completion evidence payload is required")
	}

	var data completedRunVerificationData
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return fmt.Errorf("decode completion evidence payload: %w", err)
	}
	data.SubjectRevision = strings.TrimSpace(data.SubjectRevision)
	if data.SubjectRevision == "" {
		if len(capsule.RequiredEvidence) == 0 && len(data.Evidence) == 0 {
			return nil
		}
		return errors.New("completion subject revision is required")
	}

	if _, err := advanceTaskCapsuleSubjectRevision(ctx, store, event.RunID, data.SubjectRevision); err != nil {
		return fmt.Errorf("advance completed run subject revision: %w", err)
	}
	for _, evidence := range data.Evidence {
		if _, err := recordTaskCapsuleEvidence(ctx, store, event.RunID, evidence); err != nil {
			return fmt.Errorf("record completed run evidence %q: %w", evidence.Name, err)
		}
	}
	if err := verifyTaskCapsuleCompletion(ctx, store, event.RunID); err != nil {
		return fmt.Errorf("verify completed run capsule: %w", err)
	}
	return nil
}
