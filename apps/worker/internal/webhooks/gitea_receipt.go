package webhooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/gateway"
)

// WithGiteaWebhook independently configures the worker's trusted instance and
// signing key. It does not select the forge used for PR publication.
func (c *Consumer) WithGiteaWebhook(instanceURL, secret string) *Consumer {
	c.giteaInstance, _ = gateway.GiteaWebhookInstanceURL(instanceURL)
	c.giteaSecret = strings.TrimSpace(secret)
	return c
}

func (c *Consumer) recordGiteaReceipt(ctx context.Context, event events.WebhookEvent) error {
	// Old envelopes do not carry raw signed bytes; never reinterpret them as
	// trusted receipts. Malformed or unauthenticated messages are terminal.
	if len(event.SignedPayload) == 0 {
		c.logger.Warn("discarded gitea webhook without signed bytes")
		return nil
	}
	if c.giteaInstance == "" || c.giteaSecret == "" || event.ForgeInstance != c.giteaInstance {
		return fmt.Errorf("gitea webhook worker authority not configured or does not match ingress")
	}

	if !gateway.ValidateGiteaWebhook(event.SignedPayload, event.Signature, c.giteaSecret) &&
		!gateway.ValidateGitHubWebhook(event.SignedPayload, event.Signature, c.giteaSecret) {
		c.logger.Warn("discarded gitea webhook with invalid signature")
		return nil
	}
	repo, err := gateway.GiteaWebhookReceiptIdentity(c.giteaInstance, event.DeliveryID, event.EventType, event.RepositoryID, event.SignedPayload)
	if err != nil {
		c.logger.Warn("discarded gitea webhook with invalid repository identity")
		return nil
	}

	// A signed body replay with a new delivery/type header is still a replay.
	// Unique constraints arbitrate concurrently across workers; no read-before-
	// write race. Never persist raw payloads, secrets, or reusable signatures.
	digest := sha256.Sum256(event.SignedPayload)
	result, err := c.db.ExecContext(ctx, `
        INSERT INTO gitea_webhook_receipts
            (instance_url, delivery_id, payload_sha256, repository_id, repository_full_name, event_type)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT DO NOTHING
    `, c.giteaInstance, event.DeliveryID, hex.EncodeToString(digest[:]), repo.ID, repo.FullName, event.EventType)
	if err != nil {
		return fmt.Errorf("persist gitea webhook receipt: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read gitea webhook receipt outcome: %w", err)
	}
	if count == 0 {
		c.logger.Info("duplicate or conflicting gitea webhook receipt suppressed")
	} else {
		c.logger.Info("gitea webhook receipt persisted", "repository_id", repo.ID)
	}
	// No task/merge mutations or second event publication. The committed
	// ledger is the durable observation; ACK may safely be retried.
	return nil
}
