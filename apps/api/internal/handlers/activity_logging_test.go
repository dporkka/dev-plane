package handlers

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/ai-dev-control-plane/activity"
)

type recordingActivitySink struct {
	events []activity.Event
	err    error
}

func (s *recordingActivitySink) Publish(_ context.Context, event activity.Event) error {
	s.events = append(s.events, event)
	return s.err
}

func TestActivitySinkReceivesNeutralLifecycleEvent(t *testing.T) {
	sink := &recordingActivitySink{}
	h := NewHandler(nil, slog.Default()).WithActivitySink(sink, "example-project")

	event := activity.Event{
		Type:       "dev-plane.task.created",
		Title:      "Task created",
		Text:       "details",
		ExternalID: "task:1",
	}

	h.logActivityEvent(context.Background(), event)

	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	got := sink.events[0]
	if got.Project != "example-project" {
		t.Fatalf("project = %q, want example-project", got.Project)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt must be populated before publishing")
	}
}

func TestActivitySinkFailureDoesNotBlockRequestLifecycle(t *testing.T) {
	sink := &recordingActivitySink{err: errors.New("sink unavailable")}
	h := NewHandler(nil, slog.Default()).WithActivitySink(sink, "example-project")

	before := time.Now().UTC()
	h.logActivityEvent(context.Background(), activity.Event{Type: "dev-plane.test"})
	if len(sink.events) != 1 {
		t.Fatalf("events = %d, want 1", len(sink.events))
	}
	if sink.events[0].CreatedAt.Before(before) {
		t.Fatalf("CreatedAt = %s, want >= %s", sink.events[0].CreatedAt, before)
	}
}
