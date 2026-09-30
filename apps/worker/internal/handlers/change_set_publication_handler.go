package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/ai-dev-control-plane/api/pkg/changeauthority"
	"github.com/ai-dev-control-plane/api/pkg/changesetpublisher"
	"github.com/ai-dev-control-plane/events"
)

type ChangeSetPublisher interface {
	Publish(context.Context, string, changeauthority.Actor, changesetpublisher.ProgressFunc) (*changesetpublisher.Result, error)
}

type ChangeSetPublicationHandler struct {
	db        *sql.DB
	logger    *slog.Logger
	publisher ChangeSetPublisher
}

func NewChangeSetPublicationHandler(db *sql.DB, logger *slog.Logger, eventBus WorkerEventPublisher) *ChangeSetPublicationHandler {
	if logger == nil {
		logger = slog.Default()
	}
	publisher := changesetpublisher.New(db, logger).WithEventPublisher(eventBus)
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		publisher.WithGitHubToken(token)
	}
	return &ChangeSetPublicationHandler{
		db:        db,
		logger:    logger,
		publisher: publisher,
	}
}

func (h *ChangeSetPublicationHandler) WithPublisher(publisher ChangeSetPublisher) *ChangeSetPublicationHandler {
	if publisher != nil {
		h.publisher = publisher
	}
	return h
}

func (h *ChangeSetPublicationHandler) HandlePublishRequested(msg *nats.Msg) error {
	if msg == nil {
		return errors.New("change set publication message is required")
	}

	var event events.ChangeSetPublicationEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal change set publication request: %w", err)
	}
	event.ChangeSetID = strings.TrimSpace(event.ChangeSetID)
	event.ActorID = strings.TrimSpace(event.ActorID)
	event.OrganizationID = strings.TrimSpace(event.OrganizationID)
	if event.ChangeSetID == "" || event.ActorID == "" || event.OrganizationID == "" {
		h.logger.Warn("dropping invalid change set publication request",
			"change_set_id", event.ChangeSetID,
			"actor_id", event.ActorID,
			"organization_id", event.OrganizationID,
		)
		return ackMessage(msg)
	}

	var actor changeauthority.Actor
	err := h.db.QueryRowContext(context.Background(), `
		SELECT id, organization_id, role
		FROM users
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
	`, event.ActorID, event.OrganizationID).Scan(
		&actor.UserID, &actor.OrganizationID, &actor.Role,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.logger.Warn("dropping change set publication request for revoked or unavailable actor",
				"change_set_id", event.ChangeSetID,
				"actor_id", event.ActorID,
				"organization_id", event.OrganizationID,
			)
			return ackMessage(msg)
		}
		return fmt.Errorf("reload change set publication actor: %w", err)
	}

	progress := func() error {
		if msg.Reply == "" {
			return nil
		}
		if err := msg.InProgress(); err != nil {
			return fmt.Errorf("extend change set publication message lease: %w", err)
		}
		return nil
	}

	if h.publisher == nil {
		return errors.New("change set publisher is not configured")
	}
	_, err = h.publisher.Publish(context.Background(), event.ChangeSetID, actor, progress)
	if err != nil {
		switch changesetpublisher.ErrorKindOf(err) {
		case changesetpublisher.ErrorBlocked, changesetpublisher.ErrorInvalid, changesetpublisher.ErrorAlreadyRunning:
			h.logger.Warn("change set publication request reached terminal or duplicate state",
				"change_set_id", event.ChangeSetID,
				"kind", changesetpublisher.ErrorKindOf(err),
				"error", err,
			)
			return ackMessage(msg)
		default:
			return fmt.Errorf("publish change set %s: %w", event.ChangeSetID, err)
		}
	}

	return ackMessage(msg)
}
