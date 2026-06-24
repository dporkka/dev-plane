// Package webhooks processes incoming webhook events received by the API service
// and published to the WEBHOOKS JetStream stream.
package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/models"
)

// EventPublisher publishes events to the NATS event bus.
type EventPublisher interface {
	Publish(subject string, data []byte) error
}

// Consumer processes webhook events from the WEBHOOKS stream.
type Consumer struct {
	db       *sql.DB
	logger   *slog.Logger
	eventBus EventPublisher
}

// NewConsumer creates a new webhook consumer.
func NewConsumer(db *sql.DB, logger *slog.Logger, eventBus EventPublisher) *Consumer {
	return &Consumer{
		db:       db,
		logger:   logger,
		eventBus: eventBus,
	}
}

// Handle processes a single webhook message.
func (c *Consumer) Handle(msg *nats.Msg) error {
	var event events.WebhookEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		c.logger.Error("failed to unmarshal webhook event", "error", err)
		return ack(msg)
	}

	c.logger.Info("processing webhook",
		"source", event.Source,
		"event_type", event.EventType,
		"delivery_id", event.DeliveryID,
		"repository_id", event.RepositoryID,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var err error
	switch event.Source {
	case "github":
		err = c.handleGitHub(ctx, event)
	default:
		c.logger.Info("unsupported webhook source", "source", event.Source)
	}

	if err != nil {
		c.logger.Error("failed to process webhook", "source", event.Source, "error", err)
		_ = c.publishWebhookFailed(ctx, event, err)
		return err
	}

	_ = c.publishWebhookProcessed(ctx, event)
	return ack(msg)
}

func (c *Consumer) handleGitHub(ctx context.Context, event events.WebhookEvent) error {
	switch event.EventType {
	case "issues":
		return c.handleGitHubIssue(ctx, event)
	case "push":
		// Push events can be logged for audit; task creation from pushes is not supported yet.
		c.logger.Info("github push event received", "repository", event.RepositoryID)
		return nil
	default:
		c.logger.Info("unsupported github event type", "event_type", event.EventType)
		return nil
	}
}

func (c *Consumer) handleGitHubIssue(ctx context.Context, event events.WebhookEvent) error {
	var payload struct {
		Action string `json:"action"`
		Issue  struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			State  string `json:"state"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal issue payload: %w", err)
	}

	if payload.Action != "opened" {
		c.logger.Info("github issue event ignored", "action", payload.Action)
		return nil
	}
	if payload.Issue.State == "closed" {
		c.logger.Info("github issue event ignored", "state", payload.Issue.State)
		return nil
	}

	repo, err := c.lookupRepository(ctx, event.RepositoryID)
	if err != nil {
		return fmt.Errorf("lookup repository: %w", err)
	}
	if repo == nil {
		c.logger.Warn("repository not found for webhook", "repository", event.RepositoryID)
		return nil
	}

	createdBy, err := c.lookupOrganizationUser(ctx, repo.OrganizationID)
	if err != nil {
		return fmt.Errorf("lookup organization user: %w", err)
	}
	if createdBy == "" {
		c.logger.Warn("no active user found for organization", "organization_id", repo.OrganizationID)
		return nil
	}

	taskID := uuid.New().String()
	now := time.Now().UTC()
	sourceID := fmt.Sprintf("%d", payload.Issue.Number)
	description := payload.Issue.Body

	_, err = c.db.ExecContext(ctx, `
		INSERT INTO tasks (
			id, project_id, repository_id, created_by, source, source_id,
			title, description, status, priority, risk_level, target_branch,
			spec, acceptance_criteria, max_runtime_minutes, approval_requirements,
			metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $18)
	`, taskID, repo.ProjectID, repo.ID, createdBy, models.TaskSourceGitHub, sourceID,
		payload.Issue.Title, description, models.TaskStatusBacklog, models.PriorityMedium,
		models.RiskLevelLow, repo.DefaultBranch, nil, nil, 60, nil, nil, now)
	if err != nil {
		return fmt.Errorf("create task from github issue: %w", err)
	}

	c.logger.Info("created task from github issue",
		"task_id", taskID,
		"repository", event.RepositoryID,
		"issue_number", payload.Issue.Number,
	)

	if c.eventBus != nil {
		eventPayload, _ := json.Marshal(events.TaskEvent{
			TaskID:    taskID,
			Status:    string(models.TaskStatusBacklog),
			ProjectID: repo.ProjectID,
			ActorID:   createdBy,
		})
		if pubErr := c.eventBus.Publish(events.TaskCreated, eventPayload); pubErr != nil {
			c.logger.Warn("failed to publish task created event", "error", pubErr)
		}
	}

	return nil
}

type repositoryRecord struct {
	ID             string
	ProjectID      string
	OrganizationID string
	DefaultBranch  string
}

func (c *Consumer) lookupRepository(ctx context.Context, fullName string) (*repositoryRecord, error) {
	var repo repositoryRecord
	err := c.db.QueryRowContext(ctx, `
		SELECT r.id, r.project_id, p.organization_id, r.default_branch
		FROM repositories r
		JOIN projects p ON p.id = r.project_id
		WHERE r.full_name = $1 AND r.deleted_at IS NULL
	`, fullName).Scan(&repo.ID, &repo.ProjectID, &repo.OrganizationID, &repo.DefaultBranch)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &repo, nil
}

func (c *Consumer) lookupOrganizationUser(ctx context.Context, organizationID string) (string, error) {
	var userID string
	err := c.db.QueryRowContext(ctx, `
		SELECT id FROM users
		WHERE organization_id = $1 AND deleted_at IS NULL
		ORDER BY created_at ASC
		LIMIT 1
	`, organizationID).Scan(&userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return userID, nil
}

func (c *Consumer) publishWebhookProcessed(ctx context.Context, event events.WebhookEvent) error {
	if c.eventBus == nil {
		return nil
	}
	payload, _ := json.Marshal(events.WebhookEvent{
		Source:     event.Source,
		EventType:  event.EventType,
		DeliveryID: event.DeliveryID,
		Payload:    event.Payload,
	})
	return c.eventBus.Publish(events.WebhookProcessed, payload)
}

func (c *Consumer) publishWebhookFailed(ctx context.Context, event events.WebhookEvent, err error) error {
	if c.eventBus == nil {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"source":      event.Source,
		"event_type":  event.EventType,
		"delivery_id": event.DeliveryID,
		"error":       err.Error(),
	})
	return c.eventBus.Publish(events.WebhookFailed, payload)
}

func ack(msg *nats.Msg) error {
	if msg == nil || msg.Reply == "" {
		return nil
	}
	if err := msg.Ack(); err != nil && err != nats.ErrMsgNoReply {
		return err
	}
	return nil
}
