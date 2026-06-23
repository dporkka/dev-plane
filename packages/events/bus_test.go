package events

import (
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestDefaultStreamConfigs(t *testing.T) {
	streams := DefaultStreamConfigs()

	byName := make(map[string]nats.StreamConfig, len(streams))
	for _, cfg := range streams {
		if cfg.Name == "" {
			t.Fatal("stream name must not be empty")
		}
		if _, ok := byName[cfg.Name]; ok {
			t.Fatalf("duplicate stream config for %s", cfg.Name)
		}
		if cfg.Storage != nats.FileStorage {
			t.Fatalf("stream %s storage = %v, want FileStorage", cfg.Name, cfg.Storage)
		}
		if len(cfg.Subjects) == 0 {
			t.Fatalf("stream %s must configure at least one subject", cfg.Name)
		}
		byName[cfg.Name] = cfg
	}

	expectedRetention := map[string]nats.RetentionPolicy{
		StreamTasks:    nats.WorkQueuePolicy,
		StreamAgents:   nats.WorkQueuePolicy,
		StreamRuns:     nats.WorkQueuePolicy,
		StreamWebhooks: nats.WorkQueuePolicy,
		StreamAudit:    nats.WorkQueuePolicy,
	}
	for stream, want := range expectedRetention {
		cfg, ok := byName[stream]
		if !ok {
			t.Fatalf("missing stream %s", stream)
		}
		if cfg.Retention != want {
			t.Fatalf("stream %s retention = %v, want %v", stream, cfg.Retention, want)
		}
	}

	expected := map[string][]string{
		StreamTasks:    {"tasks.*"},
		StreamAgents:   {"agents.>"},
		StreamRuns:     {"runs.*", "review.*", "approval.*", "pr.*"},
		StreamWebhooks: {"webhooks.*"},
		StreamAudit:    {"audit.>"},
	}
	for stream, subjects := range expected {
		cfg, ok := byName[stream]
		if !ok {
			t.Fatalf("missing stream %s", stream)
		}
		if strings.Join(cfg.Subjects, ",") != strings.Join(subjects, ",") {
			t.Fatalf("stream %s subjects = %v, want %v", stream, cfg.Subjects, subjects)
		}
	}
}

func TestEventSubjectsAreCoveredByConfiguredStreams(t *testing.T) {
	subjects := []string{
		TaskCreated,
		TaskUpdated,
		TaskApproved,
		TaskStarted,
		TaskCompleted,
		TaskFailed,
		TaskCancelled,
		AgentRunStarted,
		AgentRunCompleted,
		AgentRunFailed,
		AgentRunCancelled,
		AgentStepCreated,
		AgentStepCompleted,
		AgentStepFailed,
		RunTriggered,
		RunAssigned,
		RunFinished,
		WebhookReceived,
		WebhookProcessed,
		WebhookFailed,
		AuditActionLogged,
		ReviewTriggered,
		ReviewCompleted,
		ApprovalRequested,
		ApprovalApproved,
		ApprovalRejected,
		PRCreated,
	}

	for _, subject := range subjects {
		if !subjectCovered(subject, DefaultStreamConfigs()) {
			t.Fatalf("subject %q is not covered by any stream config", subject)
		}
	}
}

func subjectCovered(subject string, streams []nats.StreamConfig) bool {
	for _, stream := range streams {
		for _, pattern := range stream.Subjects {
			if subjectMatches(pattern, subject) {
				return true
			}
		}
	}
	return false
}

func subjectMatches(pattern, subject string) bool {
	patternParts := strings.Split(pattern, ".")
	subjectParts := strings.Split(subject, ".")

	for i, patternPart := range patternParts {
		if patternPart == ">" {
			return i < len(subjectParts)
		}
		if i >= len(subjectParts) {
			return false
		}
		if patternPart != "*" && patternPart != subjectParts[i] {
			return false
		}
	}

	return len(patternParts) == len(subjectParts)
}
