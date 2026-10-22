package agentruntimebootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
	"github.com/ai-dev-control-plane/api/pkg/agentapproval"
	"github.com/ai-dev-control-plane/api/pkg/agentexternal"
	"github.com/ai-dev-control-plane/events"
)

type bootstrapPersistence struct {
	mu      sync.Mutex
	threads map[string]agentruntime.Thread
	turns   map[string]agentruntime.Turn
	items   map[string]agentruntime.Item
	records []agentruntime.RuntimeEventRecord
	order   *[]string
}

func newBootstrapPersistence(order *[]string) *bootstrapPersistence {
	return &bootstrapPersistence{
		threads: make(map[string]agentruntime.Thread),
		turns:   make(map[string]agentruntime.Turn),
		items:   make(map[string]agentruntime.Item),
		order:   order,
	}
}

func (s *bootstrapPersistence) PutThread(_ context.Context, thread agentruntime.Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads[thread.ID] = thread
	return nil
}

func (s *bootstrapPersistence) GetThread(_ context.Context, id string) (agentruntime.Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, ok := s.threads[id]
	if !ok {
		return agentruntime.Thread{}, errors.New("thread not found")
	}
	return thread, nil
}

func (s *bootstrapPersistence) PutTurn(_ context.Context, turn agentruntime.Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turns[turn.ID] = turn
	return nil
}

func (s *bootstrapPersistence) GetTurn(_ context.Context, id string) (agentruntime.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.turns[id]
	if !ok {
		return agentruntime.Turn{}, errors.New("turn not found")
	}
	return turn, nil
}

func (s *bootstrapPersistence) ListTurns(_ context.Context, threadID string) ([]agentruntime.Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var turns []agentruntime.Turn
	for _, turn := range s.turns {
		if turn.ThreadID == threadID {
			turns = append(turns, turn)
		}
	}
	return turns, nil
}

func (s *bootstrapPersistence) PutItem(_ context.Context, item agentruntime.Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[item.ID] = item
	return nil
}

func (s *bootstrapPersistence) GetItem(_ context.Context, id string) (agentruntime.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return agentruntime.Item{}, errors.New("item not found")
	}
	return item, nil
}

func (s *bootstrapPersistence) ListItems(_ context.Context, turnID string) ([]agentruntime.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []agentruntime.Item
	for _, item := range s.items {
		if item.TurnID == turnID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *bootstrapPersistence) AppendRuntimeEvent(_ context.Context, record agentruntime.RuntimeEventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	if s.order != nil {
		*s.order = append(*s.order, "ledger:"+string(record.Type))
	}
	return nil
}

func (s *bootstrapPersistence) ListRuntimeEvents(_ context.Context, threadID, turnID string) ([]agentruntime.RuntimeEventRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var records []agentruntime.RuntimeEventRecord
	for _, record := range s.records {
		if record.ThreadID != threadID {
			continue
		}
		if turnID != "" && record.TurnID != turnID {
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

type bootstrapProvider struct{}

func (bootstrapProvider) Name() string { return "fake" }
func (bootstrapProvider) Capabilities() agentruntime.CapabilitySet { return 0 }
func (bootstrapProvider) CreateThread(_ context.Context, req agentruntime.CreateThreadRequest) (*agentruntime.Thread, error) {
	return &agentruntime.Thread{
		ID:          "thread-1",
		WorkspaceID: req.WorkspaceID,
		Provider:    "fake",
		Status:      agentruntime.ThreadStatusActive,
		Metadata:    append(json.RawMessage(nil), req.Metadata...),
	}, nil
}
func (bootstrapProvider) RunTurn(_ context.Context, req agentruntime.RunTurnRequest) (<-chan agentruntime.Event, error) {
	out := make(chan agentruntime.Event, 2)
	now := time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)
	out <- agentruntime.Event{
		Sequence:   1,
		Type:       agentruntime.EventTypeTurnStarted,
		ThreadID:   req.ThreadID,
		TurnID:     "turn-1",
		Status:     agentruntime.TurnStatusRunning,
		OccurredAt: now,
	}
	out <- agentruntime.Event{
		Sequence: 2,
		Type:     agentruntime.EventTypeItemStarted,
		ThreadID: req.ThreadID,
		TurnID:   "turn-1",
		Status:   agentruntime.TurnStatusPausedApproval,
		Item: &agentruntime.Item{
			ID:        "approval-item-1",
			ThreadID:  req.ThreadID,
			TurnID:    "turn-1",
			Type:      agentruntime.ItemTypeApproval,
			Status:    agentruntime.ItemStatusPending,
			Name:      "commandApproval",
			Payload:   json.RawMessage(`{"command":"make deploy"}`),
			CreatedAt: now.Add(time.Second),
		},
		OccurredAt: now.Add(time.Second),
	}
	close(out)
	return out, nil
}

type bootstrapCompletionGate struct{}
func (bootstrapCompletionGate) VerifyExternalRunCompletion(context.Context, agentexternal.CompletionRequest) error {
	return nil
}

type bootstrapPublisher struct {
	mu       sync.Mutex
	subjects []string
	order    *[]string
}

func (p *bootstrapPublisher) Publish(subject string, _ []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subjects = append(p.subjects, subject)
	if p.order != nil {
		*p.order = append(*p.order, "publish:"+subject)
	}
	return nil
}

func newBootstrapSQLDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	db, _ := newBootstrapSQLDB(t)
	persistence := newBootstrapPersistence(nil)
	publisher := &bootstrapPublisher{}
	provider := bootstrapProvider{}
	gate := bootstrapCompletionGate{}

	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "database", cfg: Config{Persistence: persistence, Publisher: publisher, CompletionGate: gate, Providers: []agentruntime.Provider{provider}}},
		{name: "persistence", cfg: Config{Database: db, Publisher: publisher, CompletionGate: gate, Providers: []agentruntime.Provider{provider}}},
		{name: "publisher", cfg: Config{Database: db, Persistence: persistence, CompletionGate: gate, Providers: []agentruntime.Provider{provider}}},
		{name: "completion gate", cfg: Config{Database: db, Persistence: persistence, Publisher: publisher, Providers: []agentruntime.Provider{provider}}},
		{name: "provider", cfg: Config{Database: db, Persistence: persistence, Publisher: publisher, CompletionGate: gate}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg); err == nil {
				t.Fatalf("New() error = nil for missing %s", tt.name)
			}
		})
	}
}

func TestNewWiresLedgerBeforeApprovalPublication(t *testing.T) {
	db, mock := newBootstrapSQLDB(t)
	order := []string{}
	persistence := newBootstrapPersistence(&order)
	publisher := &bootstrapPublisher{order: &order}

	mock.ExpectQuery(`SELECT t.created_by`).
		WithArgs("task-1", "run-1").
		WillReturnRows(sqlmock.NewRows([]string{"created_by"}).AddRow("user-1"))
	mock.ExpectExec(`INSERT INTO approvals`).
		WithArgs(sqlmock.AnyArg(), "task-1", "run-1", "agent_runtime", "user-1", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(`UPDATE agent_runs`).
		WithArgs("waiting for external agent approval", sqlmock.AnyArg(), "run-1", "task-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	bundle, err := New(Config{
		Database:       db,
		Persistence:    persistence,
		Publisher:      publisher,
		CompletionGate: bootstrapCompletionGate{},
		Providers:      []agentruntime.Provider{bootstrapProvider{}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if bundle.Manager == nil || bundle.Supervisor == nil {
		t.Fatalf("bundle = %#v", bundle)
	}

	ownership, err := json.Marshal(agentapproval.Ownership{TaskID: "task-1", AgentRunID: "run-1"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	thread, err := bundle.Manager.CreateThread(context.Background(), "fake", agentruntime.CreateThreadRequest{
		WorkspaceID: "workspace-1",
		Metadata:    ownership,
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	stream, err := bundle.Manager.RunTurn(context.Background(), agentruntime.RunTurnRequest{
		ThreadID: thread.ID,
		Input:    agentruntime.TurnInput{Text: "ship it"},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	for range stream.Events {
	}
	for streamErr := range stream.Errors {
		if streamErr != nil {
			t.Fatalf("runtime stream error = %v", streamErr)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}

	persistence.mu.Lock()
	recordCount := len(persistence.records)
	persistence.mu.Unlock()
	if recordCount != 2 {
		t.Fatalf("runtime ledger records = %d, want 2", recordCount)
	}
	publisher.mu.Lock()
	subjects := append([]string(nil), publisher.subjects...)
	publisher.mu.Unlock()
	if !reflect.DeepEqual(subjects, []string{events.ApprovalRequested}) {
		t.Fatalf("published subjects = %#v", subjects)
	}

	wantOrder := []string{
		"ledger:turn_started",
		"ledger:item_started",
		"publish:" + events.ApprovalRequested,
	}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("side-effect order = %#v, want %#v", order, wantOrder)
	}
}
