package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
)

// NotificationHandler sends notifications to connected Slack/Discord integrations.
type NotificationHandler struct {
	db       *sql.DB
	logger   *slog.Logger
	eventBus WorkerEventPublisher
}

// NewNotificationHandler creates a new notification handler.
func NewNotificationHandler(db *sql.DB, logger *slog.Logger, eventBus WorkerEventPublisher) *NotificationHandler {
	return &NotificationHandler{db: db, logger: logger, eventBus: eventBus}
}

// HandleApprovalRequested processes approval.requested events.
func (h *NotificationHandler) HandleApprovalRequested(msg *nats.Msg) error {
	var event map[string]any
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal approval requested event: %w", err)
	}

	taskID, _ := event["task_id"].(string)
	approvalType, _ := event["approval_type"].(string)
	if taskID == "" {
		return ackMessage(msg)
	}

	orgID, err := h.lookupTaskOrganization(taskID)
	if err != nil {
		return fmt.Errorf("lookup task organization: %w", err)
	}
	if orgID == "" {
		return ackMessage(msg)
	}

	message := fmt.Sprintf("Approval requested for task %s (type: %s)", taskID, approvalType)
	if err := h.sendNotifications(context.Background(), orgID, message); err != nil {
		h.logger.Warn("failed to send approval notification", "error", err)
	}
	return ackMessage(msg)
}

// HandleTaskCompleted processes task.completed events.
func (h *NotificationHandler) HandleTaskCompleted(msg *nats.Msg) error {
	var event events.TaskEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal task event: %w", err)
	}

	orgID, err := h.lookupTaskOrganization(event.TaskID)
	if err != nil {
		return fmt.Errorf("lookup task organization: %w", err)
	}
	if orgID == "" {
		return ackMessage(msg)
	}

	message := fmt.Sprintf("Task %s completed with status %s", event.TaskID, event.Status)
	if err := h.sendNotifications(context.Background(), orgID, message); err != nil {
		h.logger.Warn("failed to send task completed notification", "error", err)
	}
	return ackMessage(msg)
}

// HandleTaskFailed processes task.failed events.
func (h *NotificationHandler) HandleTaskFailed(msg *nats.Msg) error {
	var event events.TaskEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal task event: %w", err)
	}

	orgID, err := h.lookupTaskOrganization(event.TaskID)
	if err != nil {
		return fmt.Errorf("lookup task organization: %w", err)
	}
	if orgID == "" {
		return ackMessage(msg)
	}

	message := fmt.Sprintf("Task %s failed", event.TaskID)
	if err := h.sendNotifications(context.Background(), orgID, message); err != nil {
		h.logger.Warn("failed to send task failed notification", "error", err)
	}
	return ackMessage(msg)
}

func (h *NotificationHandler) lookupTaskOrganization(taskID string) (string, error) {
	var orgID string
	err := h.db.QueryRow(`
		SELECT p.organization_id
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		WHERE t.id = $1 AND t.deleted_at IS NULL
	`, taskID).Scan(&orgID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return orgID, nil
}

func (h *NotificationHandler) sendNotifications(ctx context.Context, organizationID, message string) error {
	rows, err := h.db.QueryContext(ctx, `
		SELECT integration_type, credentials_encrypted, config
		FROM integrations
		WHERE organization_id = $1 AND status = 'connected' AND deleted_at IS NULL
		  AND integration_type IN ('slack', 'discord')
	`, organizationID)
	if err != nil {
		return fmt.Errorf("query integrations: %w", err)
	}
	defer rows.Close()

	var firstErr error
	for rows.Next() {
		var integrationType string
		var credentials sql.NullString
		var config sql.NullString
		if err := rows.Scan(&integrationType, &credentials, &config); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}

		token := ""
		if credentials.Valid {
			token = credentials.String
		}

		channelID := ""
		if config.Valid && config.String != "" {
			var cfg struct {
				ChannelID  string `json:"channel_id"`
				WebhookURL string `json:"webhook_url"`
			}
			_ = json.Unmarshal([]byte(config.String), &cfg)
			if channelID == "" {
				channelID = cfg.ChannelID
			}
		}

		switch integrationType {
		case "slack":
			if token == "" {
				continue
			}
			g := gateway.NewSlackGateway(token)
			if err := g.PostMessage(ctx, channelID, message); err != nil {
				h.logger.Warn("failed to post slack message", "error", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		case "discord":
			webhookURL := ""
			if config.Valid && config.String != "" {
				var cfg struct {
					WebhookURL string `json:"webhook_url"`
				}
				_ = json.Unmarshal([]byte(config.String), &cfg)
				webhookURL = cfg.WebhookURL
			}
			g := gateway.NewDiscordGateway(token, webhookURL)
			if webhookURL != "" {
				if err := g.SendWebhook(ctx, message); err != nil {
					h.logger.Warn("failed to send discord webhook", "error", err)
					if firstErr == nil {
						firstErr = err
					}
				}
			} else if token != "" && channelID != "" {
				if err := g.SendMessage(ctx, channelID, message); err != nil {
					h.logger.Warn("failed to send discord message", "error", err)
					if firstErr == nil {
						firstErr = err
					}
				}
			}
		}
	}
	return firstErr
}
