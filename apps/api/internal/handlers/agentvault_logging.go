package handlers

import (
	"context"
	"fmt"
	"time"

	agentvaultclient "github.com/ai-dev-control-plane/api/internal/agentvault"
)

func (h *Handler) logAgentVaultEvent(ctx context.Context, event agentvaultclient.Event) {
	if h.agentVault == nil {
		return
	}
	if event.Project == "" {
		event.Project = h.agentVaultProject
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if err := h.agentVault.LogEvent(ctx, event); err != nil {
		h.logger.Warn("failed to log event to agentvault", "error", err, "event_type", event.Type)
	}
}

func taskCreatedEvent(task Task, source string) agentvaultclient.Event {
	text := fmt.Sprintf("Task `%s` was created in Dev Plane.\n\nRepository: `%s`\nStatus: `%s`\nPriority: `%s`",
		task.Title, task.RepositoryID, task.Status, task.Priority)
	if task.Description != nil && *task.Description != "" {
		text += "\n\n" + *task.Description
	}
	return agentvaultclient.Event{
		Type:  "dev-plane.task.created",
		Title: "Dev Plane task created: " + task.Title,
		Text:  text,
		Tags:  []string{"dev-plane", "task", source},
		Metadata: map[string]any{
			"task_id":       task.ID,
			"project_id":    task.ProjectID,
			"repository_id": task.RepositoryID,
			"source":        source,
			"status":        task.Status,
		},
		CreatedAt: task.CreatedAt,
	}
}
