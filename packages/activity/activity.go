// Package activity defines provider-neutral lifecycle events and optional sinks.
package activity

import (
	"context"
	"time"
)

// Event is a high-level Dev Plane lifecycle record suitable for optional
// context, audit, observability, or external activity sinks.
type Event struct {
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	Text       string         `json:"text"`
	Project    string         `json:"project,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	ExternalID string         `json:"external_id,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Sink accepts lifecycle events. Sinks are optional integrations: callers
// decide whether delivery failures are blocking or best-effort.
type Sink interface {
	Publish(ctx context.Context, event Event) error
}
