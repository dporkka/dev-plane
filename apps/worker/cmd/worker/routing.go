package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ai-dev-control-plane/events"
	"github.com/ai-dev-control-plane/prfactory"
	"github.com/nats-io/nats.go"
)

const (
	laneShared   = "shared"
	laneGitea    = "gitea"
	laneWebhooks = "webhooks"
)

// Validate lane before connecting or subscribing. A process carrying Gitea
// credentials must never run shared tasks, even if the broker is misconfigured.
func workerRoutingConfig(runtime string) (string, bool, error) {
	lane := envOrDefault("DEV_PLANE_WORKER_LANE", laneShared)
	webhooks := os.Getenv("DEV_PLANE_WORKER_WEBHOOKS")
	if webhooks != "" && webhooks != "0" && webhooks != "1" {
		return "", false, errors.New("DEV_PLANE_WORKER_WEBHOOKS must be 0 or 1")
	}
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("FORGE_PROVIDER")))
	switch lane {
	case laneShared:
		if prfactory.GiteaCredentialsConfigured() || provider == "gitea" {
			return "", false, errors.New("shared worker cannot hold Gitea credentials; use dedicated lane")
		}
		return lane, webhooks != "0", nil
	case laneGitea:
		if runtime != "docker" || os.Getenv("RUNNER_URL") != "" || provider != "gitea" || webhooks == "1" ||
			os.Getenv("GITEA_WEBHOOK_SECRET") != "" || os.Getenv("GITEA_GIT_PASSWORD") != "" ||
			os.Getenv("GITEA_GIT_USERNAME") != "" || os.Getenv("GITHUB_TOKEN") != "" ||
			os.Getenv("SECRET_ENCRYPTION_KEYS") != "" || os.Getenv("GITHUB_APP_PRIVATE_KEY") != "" ||
			os.Getenv("GITHUB_APP_WEBHOOK_SECRET") != "" {
			return "", false, errors.New("gitea lane requires local Docker, FORGE_PROVIDER=gitea, and no webhook, GitHub or push credential")
		}
		if _, _, err := prfactory.ConfiguredGiteaBindingForge(); err != nil {
			return "", false, err
		}
		if _, err := prfactory.ConfiguredGiteaBindingGeneration(); err != nil {
			return "", false, err
		}
		cloneToken := os.Getenv("GITEA_CLONE_TOKEN")
		if cloneToken == "" || cloneToken == os.Getenv("GITEA_TOKEN") || os.Getenv("GITEA_CLONE_USERNAME") == "" ||
			os.Getenv("GITEA_CLONE_URL") == "" || os.Getenv("GITEA_CLONE_REPOSITORY_ID") == "" {
			return "", false, errors.New("dedicated scoped clone credential configuration required")
		}
		return lane, false, nil
	case laneWebhooks:
		if webhooks == "0" || provider == "gitea" || os.Getenv("GITEA_TOKEN") != "" ||
			os.Getenv("GITEA_CLONE_TOKEN") != "" || os.Getenv("GITEA_GIT_PASSWORD") != "" || os.Getenv("GITEA_GIT_USERNAME") != "" {
			return "", false, errors.New("webhooks lane cannot hold Gitea API, clone or push credentials")
		}
		return lane, true, nil
	default:
		return "", false, errors.New("DEV_PLANE_WORKER_LANE must be shared, gitea, or webhooks")
	}
}

type eventHandler func(*nats.Msg) error

type workerHandlers struct {
	taskCreated, taskApproved, taskCompleted, taskFailed           eventHandler
	runTriggered, runCompleted, runFailed, reviewCompleted         eventHandler
	approvalApproved, approvalRejected, approvalRequested, webhook eventHandler
}

func registerWorkerSubscriptions(db *sql.DB, bus *events.Bus, lane string, h workerHandlers, shutdown *shutdownContext) error {
	// Explicit registration means the dedicated worker never receives shared
	// jobs, notifications, handoffs or webhook payloads.
	type subscription struct {
		subject string
		handle  eventHandler
		manual  bool
	}
	var subs []subscription
	switch lane {
	case laneShared:
		// Keep the existing tasks_star durable. WorkQueue retention forbids
		// overlapping task notification filters, so dispatch them here too.
		subs = []subscription{
			{"tasks.*", func(msg *nats.Msg) error {
				switch msg.Subject {
				case events.TaskCreated:
					return h.taskCreated(msg)
				case events.TaskApproved:
					return guardExecutionDelivery(db, false, h.taskApproved)(msg)
				case events.TaskCompleted:
					return h.taskCompleted(msg)
				case events.TaskFailed:
					return h.taskFailed(msg)
				default:
					return msg.Ack()
				}
			}, true},
			{events.AgentRunCompleted, h.runCompleted, false},
			{events.AgentRunFailed, h.runFailed, false},
			{events.ReviewCompleted, h.reviewCompleted, false},
			{events.ApprovalRequested, h.approvalRequested, false},
			{events.ApprovalApproved, guardExecutionDelivery(db, false, h.approvalApproved), true},
			{events.ApprovalRejected, guardExecutionDelivery(db, false, h.approvalRejected), true},
			{events.RunTriggered, guardExecutionDelivery(db, false, h.runTriggered), true},
		}
	case laneGitea:
		subs = []subscription{
			{events.GiteaTaskApproved, guardExecutionDelivery(db, true, h.taskApproved), true},
			{events.GiteaRunTriggered, guardExecutionDelivery(db, true, h.runTriggered), true},
			{events.GiteaApprovalApproved, guardExecutionDelivery(db, true, h.approvalApproved), true},
			{events.GiteaApprovalRejected, guardExecutionDelivery(db, true, h.approvalRejected), true},
		}
		if h.webhook != nil {
			return errors.New("gitea execution lane cannot consume webhooks")
		}
	case laneWebhooks:
		if h.webhook == nil {
			return errors.New("webhooks lane requires consumer")
		}
	default:
		return errors.New("unknown worker lane")
	}
	if h.webhook != nil {
		subs = append(subs, subscription{"webhooks.*", h.webhook, true})
	}
	for _, s := range subs {
		if s.handle == nil {
			return fmt.Errorf("missing handler for %s", s.subject)
		}
		callback := func(msg *nats.Msg) {
			if err := s.handle(msg); err != nil {
				shutdown.logger.Error("event handler failed", "subject", msg.Subject, "error", err)
			}
		}
		var sub *nats.Subscription
		var err error
		switch {
		case lane == laneGitea:
			// Dedicated durables are new. Give the bounded 30-minute
			// provisioning/run handler time to ACK without redelivery.
			durable := strings.ReplaceAll(s.subject, ".", "_")
			sub, err = bus.JetStream().Subscribe(s.subject, callback, nats.Durable(durable), nats.ManualAck(), nats.AckWait(35*time.Minute), nats.MaxAckPending(1))
		case s.manual:
			sub, err = bus.SubscribeManual(s.subject, callback)
		default:
			sub, err = bus.Subscribe(s.subject, callback)
		}
		if err != nil {
			return err
		}
		shutdown.addSubscription(sub)
		shutdown.logger.Info("subscribed to worker subject", "subject", s.subject, "lane", lane)
	}
	return nil
}

func guardExecutionDelivery(db *sql.DB, gitea bool, handler eventHandler) eventHandler {
	return func(msg *nats.Msg) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := events.VerifyExecutionDelivery(ctx, db, msg.Subject, msg.Data, gitea)
		cancel()
		if err != nil {
			// Do not ACK/drop or forward a mismatched event; retain it on
			// its original queue. Slow down repeated failures for review.
			if msg.Reply != "" {
				if nakErr := msg.NakWithDelay(time.Minute); nakErr != nil {
					return fmt.Errorf("%w: could not defer delivery", events.ErrExecutionRoute)
				}
			}
			return events.ErrExecutionRoute
		}
		return handler(msg)
	}
}
