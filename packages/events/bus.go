package events

import (
	"fmt"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"
)

// Stream names used across the system.
const (
	StreamTasks    = "TASKS"
	StreamAgents   = "AGENTS"
	StreamRuns     = "RUNS"
	StreamWebhooks = "WEBHOOKS"
	StreamAudit    = "AUDIT"
)

// Bus wraps a NATS connection and JetStream context to provide
// typed stream management, publishing, and subscription.
type Bus struct {
	nc      *nats.Conn
	js      nats.JetStreamContext
	streams map[string]nats.StreamConfig
	mu      sync.RWMutex
}

// New connects to NATS at the given URL and creates a JetStream context.
func New(url string) (*Bus, error) {
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("create jetstream context: %w", err)
	}
	return &Bus{
		nc:      nc,
		js:      js,
		streams: make(map[string]nats.StreamConfig),
	}, nil
}

// DefaultStreamConfigs returns the JetStream streams required by the platform.
func DefaultStreamConfigs() []nats.StreamConfig {
	return []nats.StreamConfig{
		{
			Name:      StreamTasks,
			Subjects:  []string{"tasks.*"},
			Storage:   nats.FileStorage,
			Retention: nats.InterestPolicy,
		},
		{
			Name:      StreamAgents,
			Subjects:  []string{"agents.>"},
			Storage:   nats.FileStorage,
			Retention: nats.InterestPolicy,
		},
		{
			Name:      StreamRuns,
			Subjects:  []string{"runs.*", "review.*", "approval.*", "pr.*"},
			Storage:   nats.FileStorage,
			Retention: nats.InterestPolicy,
		},
		{
			Name:      StreamWebhooks,
			Subjects:  []string{"webhooks.*"},
			Storage:   nats.FileStorage,
			Retention: nats.WorkQueuePolicy,
		},
		{
			Name:      StreamAudit,
			Subjects:  []string{"audit.>"},
			Storage:   nats.FileStorage,
			Retention: nats.WorkQueuePolicy,
		},
	}
}

// CreateStreams idempotently creates all required JetStream streams.
// If a stream already exists, it is skipped without error.
func (b *Bus) CreateStreams() error {
	for _, cfg := range DefaultStreamConfigs() {
		_, err := b.js.AddStream(&cfg)
		if err != nil {
			// Stream may already exist; check for nats-specific error
			if strings.Contains(err.Error(), "already in use") ||
				strings.Contains(err.Error(), "stream name already in use") {
				// Stream exists, update our local map
				b.mu.Lock()
				b.streams[cfg.Name] = cfg
				b.mu.Unlock()
				continue
			}
			return fmt.Errorf("create stream %q: %w", cfg.Name, err)
		}

		b.mu.Lock()
		b.streams[cfg.Name] = cfg
		b.mu.Unlock()
	}
	return nil
}

// Publish sends a message to the given JetStream subject.
func (b *Bus) Publish(subject string, data []byte) error {
	_, err := b.js.Publish(subject, data)
	if err != nil {
		return fmt.Errorf("publish to %q: %w", subject, err)
	}
	return nil
}

// Subscribe creates a durable subscription on the given subject.
func (b *Bus) Subscribe(subject string, handler nats.MsgHandler) (*nats.Subscription, error) {
	// Use subject as durable name (sanitized for NATS naming rules)
	durableName := strings.NewReplacer(".", "_", "*", "star", ">", "all").Replace(subject)
	sub, err := b.js.Subscribe(subject, handler, nats.Durable(durableName))
	if err != nil {
		return nil, fmt.Errorf("subscribe to %q: %w", subject, err)
	}
	return sub, nil
}

// SubscribeQueue creates a queue-subscription for load-balanced consumers.
func (b *Bus) SubscribeQueue(subject, queue string, handler nats.MsgHandler) (*nats.Subscription, error) {
	sub, err := b.js.QueueSubscribe(subject, queue, handler, nats.Durable(queue+"-worker"))
	if err != nil {
		return nil, fmt.Errorf("queue subscribe to %q on %q: %w", subject, queue, err)
	}
	return sub, nil
}

// GetStreamInfo returns information about a stream by name.
func (b *Bus) GetStreamInfo(name string) (*nats.StreamInfo, error) {
	return b.js.StreamInfo(name)
}

// Conn returns the underlying NATS connection (for advanced use).
func (b *Bus) Conn() *nats.Conn {
	return b.nc
}

// JetStream returns the underlying JetStream context (for advanced use).
func (b *Bus) JetStream() nats.JetStreamContext {
	return b.js
}

// Close drains and closes the NATS connection.
func (b *Bus) Close() error {
	b.nc.Close()
	return nil
}
