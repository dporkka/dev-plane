// Package webhooks processes incoming webhook events received by the API service
// and published to the WEBHOOKS JetStream stream.
package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	case "linear":
		err = c.handleLinear(ctx, event)
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

	sourceID := fmt.Sprintf("%d", payload.Issue.Number)

	taskID, err := c.createTask(ctx, createTaskRequest{
		projectID:     repo.ProjectID,
		repositoryID:  repo.ID,
		createdBy:     createdBy,
		source:        models.TaskSourceGitHub,
		sourceID:      sourceID,
		title:         payload.Issue.Title,
		description:   payload.Issue.Body,
		targetBranch:  repo.DefaultBranch,
	})
	if err != nil {
		return err
	}

	c.logger.Info("created task from github issue",
		"task_id", taskID,
		"repository", event.RepositoryID,
		"issue_number", payload.Issue.Number,
	)

	c.publishTaskCreated(taskID, repo.ProjectID, createdBy)
	return nil
}

func (c *Consumer) handleLinear(ctx context.Context, event events.WebhookEvent) error {
	switch event.EventType {
	case "Issue":
		return c.handleLinearIssue(ctx, event)
	default:
		c.logger.Info("unsupported linear event type", "event_type", event.EventType)
		return nil
	}
}

func (c *Consumer) handleLinearIssue(ctx context.Context, event events.WebhookEvent) error {
	var payload struct {
		Action string `json:"action"`
		Type   string `json:"type"`
		Data   struct {
			ID          string `json:"id"`
			Identifier  string `json:"identifier"`
			Title       string `json:"title"`
			Description string `json:"description"`
			State       struct {
				Name string `json:"name"`
			} `json:"state"`
			Team struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Key  string `json:"key"`
			} `json:"team"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal linear issue payload: %w", err)
	}

	if payload.Action != "create" && payload.Action != "update" {
		c.logger.Info("linear issue event ignored", "action", payload.Action)
		return nil
	}
	if strings.EqualFold(payload.Data.State.Name, "canceled") || strings.EqualFold(payload.Data.State.Name, "done") {
		c.logger.Info("linear issue event ignored", "state", payload.Data.State.Name)
		return nil
	}
	if payload.Data.ID == "" || payload.Data.Identifier == "" {
		c.logger.Info("linear issue event missing required identifiers")
		return nil
	}

	integration, err := c.lookupLinearIntegration(ctx, payload.Data.Team.ID)
	if err != nil {
		return fmt.Errorf("lookup linear integration: %w", err)
	}
	if integration == nil {
		c.logger.Warn("no linear integration found for team", "team_id", payload.Data.Team.ID)
		return nil
	}

	repo, err := c.lookupRepositoryByID(ctx, integration.Config.RepositoryID)
	if err != nil {
		return fmt.Errorf("lookup repository: %w", err)
	}
	if repo == nil {
		c.logger.Warn("repository not found for linear integration", "repository_id", integration.Config.RepositoryID)
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

	metadata, _ := json.Marshal(map[string]any{
		"linear_team_id":    payload.Data.Team.ID,
		"linear_team_name":  payload.Data.Team.Name,
		"linear_team_key":   payload.Data.Team.Key,
		"linear_issue_url":  payload.Data.URL,
		"linear_issue_id":   payload.Data.ID,
	})

	taskID, err := c.createTask(ctx, createTaskRequest{
		projectID:     integration.Config.ProjectID,
		repositoryID:  integration.Config.RepositoryID,
		createdBy:     createdBy,
		source:        models.TaskSourceLinear,
		sourceID:      payload.Data.Identifier,
		title:         payload.Data.Title,
		description:   payload.Data.Description,
		targetBranch:  repo.DefaultBranch,
		metadata:      metadata,
	})
	if err != nil {
		return err
	}

	c.logger.Info("created task from linear issue",
		"task_id", taskID,
		"linear_issue", payload.Data.Identifier,
		"repository", integration.Config.RepositoryID,
	)

	c.publishTaskCreated(taskID, integration.Config.ProjectID, createdBy)
	return nil
}

type linearIntegrationConfig struct {
	TeamID       string `json:"team_id"`
	ProjectID    string `json:"project_id"`
	RepositoryID string `json:"repository_id"`
}

type linearIntegrationRecord struct {
	ID             string
	OrganizationID string
	Config         linearIntegrationConfig
}

func (c *Consumer) lookupLinearIntegration(ctx context.Context, teamID string) (*linearIntegrationRecord, error) {
	if teamID == "" {
		return nil, nil
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT id, organization_id, config
		FROM integrations
		WHERE integration_type = 'linear' AND deleted_at IS NULL AND status = 'connected'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var record linearIntegrationRecord
		var configJSON string
		if err := rows.Scan(&record.ID, &record.OrganizationID, &configJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(configJSON), &record.Config); err != nil {
			c.logger.Warn("failed to unmarshal linear integration config", "integration_id", record.ID, "error", err)
			continue
		}
		if record.Config.TeamID == teamID {
			return &record, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

type createTaskRequest struct {
	projectID    string
	repositoryID string
	createdBy    string
	source       string
	sourceID     string
	title        string
	description  string
	targetBranch string
	metadata     []byte
}

func (c *Consumer) createTask(ctx context.Context, req createTaskRequest) (string, error) {
	taskID := uuid.New().String()
	now := time.Now().UTC()

	var description interface{}
	if req.description != "" {
		description = req.description
	}
	var metadata interface{}
	if len(req.metadata) > 0 {
		metadata = req.metadata
	}

	_, err := c.db.ExecContext(ctx, `
		INSERT INTO tasks (
			id, project_id, repository_id, created_by, source, source_id,
			title, description, status, priority, risk_level, target_branch,
			spec, acceptance_criteria, max_runtime_minutes, approval_requirements,
			metadata, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $18)
	`, taskID, req.projectID, req.repositoryID, req.createdBy, req.source, req.sourceID,
		req.title, description, models.TaskStatusBacklog, models.PriorityMedium,
		models.RiskLevelLow, req.targetBranch, nil, nil, 60, nil, metadata, now)
	if err != nil {
		return "", fmt.Errorf("create task from %s issue: %w", req.source, err)
	}
	return taskID, nil
}

func (c *Consumer) publishTaskCreated(taskID, projectID, actorID string) {
	if c.eventBus == nil {
		return
	}
	eventPayload, _ := json.Marshal(events.TaskEvent{
		TaskID:    taskID,
		Status:    string(models.TaskStatusBacklog),
		ProjectID: projectID,
		ActorID:   actorID,
	})
	if err := c.eventBus.Publish(events.TaskCreated, eventPayload); err != nil {
		c.logger.Warn("failed to publish task created event", "error", err)
	}
}

type repositoryRecord struct {
	ID             string
	ProjectID      string
	OrganizationID string
	DefaultBranch  string
}

func (c *Consumer) lookupRepositoryByID(ctx context.Context, id string) (*repositoryRecord, error) {
	var repo repositoryRecord
	err := c.db.QueryRowContext(ctx, `
		SELECT r.id, r.project_id, p.organization_id, r.default_branch
		FROM repositories r
		JOIN projects p ON p.id = r.project_id
		WHERE r.id = $1 AND r.deleted_at IS NULL
	`, id).Scan(&repo.ID, &repo.ProjectID, &repo.OrganizationID, &repo.DefaultBranch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &repo, nil
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
		if errors.Is(err, sql.ErrNoRows) {
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
		if errors.Is(err, sql.ErrNoRows) {
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
