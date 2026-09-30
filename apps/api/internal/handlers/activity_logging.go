package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/ai-dev-control-plane/activity"
)

func (h *Handler) logActivityEvent(ctx context.Context, event activity.Event) {
	if h.activitySink == nil {
		return
	}
	if event.Project == "" {
		event.Project = h.activityProject
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if err := h.activitySink.Publish(ctx, event); err != nil {
		h.logger.Warn("failed to publish lifecycle activity event", "error", err, "event_type", event.Type)
	}
}

func taskCreatedEvent(task Task, source string) activity.Event {
	text := fmt.Sprintf("Task `%s` was created in Dev Plane.\n\nRepository: `%s`\nStatus: `%s`\nPriority: `%s`",
		task.Title, task.RepositoryID, task.Status, task.Priority)
	if task.Description != nil && *task.Description != "" {
		text += "\n\n" + *task.Description
	}
	return activity.Event{
		Type:       "dev-plane.task.created",
		Title:      "Dev Plane task created: " + task.Title,
		Text:       text,
		Tags:       []string{"dev-plane", "task", source},
		ExternalID: fmt.Sprintf("dev-plane:task:%s:created", task.ID),
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
