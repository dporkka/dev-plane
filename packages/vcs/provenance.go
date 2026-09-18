package vcs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type EventKind string

const (
	EventWorkspaceCreated EventKind = "workspace.created"
	EventSnapshotCreated  EventKind = "snapshot.created"
	EventPublished        EventKind = "change.published"
	EventWorkspaceRemoved EventKind = "workspace.removed"
)

// ProvenanceEvent is intentionally VCS-neutral so it can later be written to
// AgentVault, NATS, Dolt, Postgres, or an append-only event DAG.
type ProvenanceEvent struct {
	ID         string            `json:"id"`
	Timestamp  time.Time         `json:"timestamp"`
	Kind       EventKind         `json:"kind"`
	Backend    string            `json:"backend"`
	TaskID     string            `json:"task_id"`
	AgentID    string            `json:"agent_id"`
	Workspace  string            `json:"workspace"`
	PublishRef string            `json:"publish_ref,omitempty"`
	CommitID   string            `json:"commit_id,omitempty"`
	ChangeID   string            `json:"change_id,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type Recorder interface {
	Record(ctx context.Context, event ProvenanceEvent) error
}

type NopRecorder struct{}

func (NopRecorder) Record(context.Context, ProvenanceEvent) error { return nil }

// JSONLRecorder is a minimal durable local provenance sink. Each successful
// Record call appends and fsyncs one JSON object.
type JSONLRecorder struct {
	path string
	mu   sync.Mutex
}

func NewJSONLRecorder(path string) *JSONLRecorder { return &JSONLRecorder{path: path} }

func (r *JSONLRecorder) Record(_ context.Context, event ProvenanceEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if event.ID == "" {
		id, err := randomID()
		if err != nil {
			return err
		}
		event.ID = id
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal provenance event: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return fmt.Errorf("create provenance directory: %w", err)
	}
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open provenance log: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("append provenance event: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync provenance event: %w", err)
	}
	return nil
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate event ID: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
