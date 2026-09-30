package agentruntime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type managerStore struct {
	mu      sync.Mutex
	threads map[string]Thread
	turns   map[string]Turn
	items   map[string]Item
	putErr  error
}

func newManagerStore() *managerStore {
	return &managerStore{
		threads: make(map[string]Thread),
		turns:   make(map[string]Turn),
		items:   make(map[string]Item),
	}
}

func (s *managerStore) PutThread(_ context.Context, thread Thread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.threads[thread.ID] = thread
	return nil
}

func (s *managerStore) GetThread(_ context.Context, id string) (Thread, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	thread, ok := s.threads[id]
	if !ok {
		return Thread{}, errors.New("thread not found")
	}
	return thread, nil
}

func (s *managerStore) PutTurn(_ context.Context, turn Turn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.turns[turn.ID] = turn
	return nil
}

func (s *managerStore) GetTurn(_ context.Context, id string) (Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	turn, ok := s.turns[id]
	if !ok {
		return Turn{}, errors.New("turn not found")
	}
	return turn, nil
}

func (s *managerStore) ListTurns(_ context.Context, threadID string) ([]Turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var turns []Turn
	for _, turn := range s.turns {
		if turn.ThreadID == threadID {
			turns = append(turns, turn)
		}
	}
	return turns, nil
}

func (s *managerStore) PutItem(_ context.Context, item Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	s.items[item.ID] = item
	return nil
}

func (s *managerStore) GetItem(_ context.Context, id string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok {
		return Item{}, errors.New("item not found")
	}
	return item, nil
}

func (s *managerStore) ListItems(_ context.Context, turnID string) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var items []Item
	for _, item := range s.items {
		if item.TurnID == turnID {
			items = append(items, item)
		}
	}
	return items, nil
}

type managerProvider struct {
	lastResume ResumeThreadRequest
	lastRun    RunTurnRequest
}

func (p *managerProvider) Name() string { return "codex" }

func (p *managerProvider) Capabilities() CapabilitySet {
	return NewCapabilitySet(CapabilityResumeThread)
}

func (p *managerProvider) CreateThread(_ context.Context, req CreateThreadRequest) (*Thread, error) {
	return &Thread{
		ID:               "thread-local",
		WorkspaceID:      req.WorkspaceID,
		Provider:         "codex",
		ProviderThreadID: "thread-native",
		Model:            req.Model,
		Status:           ThreadStatusActive,
	}, nil
}

func (p *managerProvider) ResumeThread(_ context.Context, req ResumeThreadRequest) (*Thread, error) {
	p.lastResume = req
	return &Thread{
		ID:               req.ThreadID,
		WorkspaceID:      req.WorkspaceID,
		Provider:         "codex",
		ProviderThreadID: req.ProviderThreadID,
		Model:            req.Model,
		Status:           ThreadStatusActive,
	}, nil
}

func (p *managerProvider) RunTurn(ctx context.Context, req RunTurnRequest) (<-chan Event, error) {
	p.lastRun = req
	events := make(chan Event)
	go func() {
		defer close(events)
		now := time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC)
		payload := Item{
			ID:             "item-local",
			ProviderItemID: "item-native",
			Type:           ItemTypeCommand,
			Status:         ItemStatusCompleted,
			Name:           "commandExecution",
		}
		for _, event := range []Event{
			{
				Type:           EventTypeTurnStarted,
				ThreadID:       req.ThreadID,
				TurnID:         "turn-local",
				ProviderTurnID: "turn-native",
				Status:         TurnStatusRunning,
				OccurredAt:     now,
			},
			{
				Type:           EventTypeItemCompleted,
				ThreadID:       req.ThreadID,
				TurnID:         "turn-local",
				ProviderTurnID: "turn-native",
				Status:         TurnStatusRunning,
				Item:           &payload,
				OccurredAt:     now.Add(time.Second),
			},
			{
				Type:           EventTypeTurnCompleted,
				ThreadID:       req.ThreadID,
				TurnID:         "turn-local",
				ProviderTurnID: "turn-native",
				Status:         TurnStatusCompleted,
				OccurredAt:     now.Add(2 * time.Second),
			},
		} {
			select {
			case <-ctx.Done():
				return
			case events <- event:
			}
		}
	}()
	return events, nil
}

func TestManagerCreateThreadPersistsProviderState(t *testing.T) {
	store := newManagerStore()
	provider := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	thread, err := manager.CreateThread(context.Background(), "codex", CreateThreadRequest{
		WorkspaceID: "workspace-1",
		Model:       "gpt-5.6-codex",
	})
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if thread.ProviderThreadID != "thread-native" {
		t.Fatalf("ProviderThreadID = %q", thread.ProviderThreadID)
	}

	stored, err := store.GetThread(context.Background(), thread.ID)
	if err != nil {
		t.Fatalf("GetThread() error = %v", err)
	}
	if stored.ProviderThreadID != "thread-native" || stored.WorkspaceID != "workspace-1" {
		t.Fatalf("stored thread = %#v", stored)
	}
	if stored.CreatedAt.IsZero() || stored.UpdatedAt.IsZero() {
		t.Fatalf("manager should fill missing timestamps: %#v", stored)
	}
}

func TestManagerResumeThreadUsesDurableProviderIdentity(t *testing.T) {
	store := newManagerStore()
	store.threads["thread-local"] = Thread{
		ID:               "thread-local",
		WorkspaceID:      "workspace-1",
		Provider:         "codex",
		ProviderThreadID: "thread-native",
		Model:            "gpt-5.6-codex",
		Status:           ThreadStatusActive,
	}
	provider := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	resumed, err := manager.ResumeThread(context.Background(), "thread-local")
	if err != nil {
		t.Fatalf("ResumeThread() error = %v", err)
	}
	if resumed.CreatedAt.IsZero() || resumed.UpdatedAt.IsZero() {
		t.Fatalf("resumed thread timestamps should be populated: %#v", resumed)
	}

	if provider.lastResume.ThreadID != "thread-local" {
		t.Fatalf("resume ThreadID = %q", provider.lastResume.ThreadID)
	}
	if provider.lastResume.ProviderThreadID != "thread-native" {
		t.Fatalf("resume ProviderThreadID = %q", provider.lastResume.ProviderThreadID)
	}
	if provider.lastResume.WorkspaceID != "workspace-1" {
		t.Fatalf("resume WorkspaceID = %q", provider.lastResume.WorkspaceID)
	}
}

func TestManagerRunTurnInjectsProviderIdentityAndPersistsEvents(t *testing.T) {
	store := newManagerStore()
	store.threads["thread-local"] = Thread{
		ID:               "thread-local",
		WorkspaceID:      "workspace-1",
		Provider:         "codex",
		ProviderThreadID: "thread-native",
		Status:           ThreadStatusActive,
	}
	provider := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	stream, err := manager.RunTurn(context.Background(), RunTurnRequest{
		ThreadID: "thread-local",
		Input:    TurnInput{Text: "Run tests"},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}
	for range stream.Events {
	}
	if err := <-stream.Errors; err != nil {
		t.Fatalf("stream persistence error = %v", err)
	}

	if provider.lastRun.ProviderThreadID != "thread-native" {
		t.Fatalf("run ProviderThreadID = %q", provider.lastRun.ProviderThreadID)
	}

	turn, err := store.GetTurn(context.Background(), "turn-local")
	if err != nil {
		t.Fatalf("GetTurn() error = %v", err)
	}
	if turn.ProviderTurnID != "turn-native" {
		t.Fatalf("turn ProviderTurnID = %q", turn.ProviderTurnID)
	}
	if turn.Status != TurnStatusCompleted || turn.CompletedAt == nil {
		t.Fatalf("persisted turn = %#v", turn)
	}

	item, err := store.GetItem(context.Background(), "item-local")
	if err != nil {
		t.Fatalf("GetItem() error = %v", err)
	}
	if item.ThreadID != "thread-local" || item.TurnID != "turn-local" {
		t.Fatalf("persisted item ownership = %#v", item)
	}
	if item.ProviderItemID != "item-native" {
		t.Fatalf("ProviderItemID = %q", item.ProviderItemID)
	}
}

func TestManagerRunTurnFailsClosedWhenPersistenceFails(t *testing.T) {
	store := newManagerStore()
	store.threads["thread-local"] = Thread{
		ID:               "thread-local",
		WorkspaceID:      "workspace-1",
		Provider:         "codex",
		ProviderThreadID: "thread-native",
		Status:           ThreadStatusActive,
	}
	provider := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(provider); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	store.putErr = errors.New("disk unavailable")
	stream, err := manager.RunTurn(context.Background(), RunTurnRequest{
		ThreadID: "thread-local",
		Input:    TurnInput{Text: "Run tests"},
	})
	if err != nil {
		t.Fatalf("RunTurn() error = %v", err)
	}

	for range stream.Events {
	}
	if err := <-stream.Errors; err == nil {
		t.Fatal("stream persistence error = nil, want error")
	}
}
