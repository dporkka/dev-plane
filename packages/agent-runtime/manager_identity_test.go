package agentruntime

import (
	"context"
	"testing"
)

type misboundProvider struct {
	managerProvider
	created *Thread
	resumed *Thread
}

func (p *misboundProvider) CreateThread(_ context.Context, _ CreateThreadRequest) (*Thread, error) {
	thread := *p.created
	return &thread, nil
}

func (p *misboundProvider) ResumeThread(_ context.Context, _ ResumeThreadRequest) (*Thread, error) {
	thread := *p.resumed
	return &thread, nil
}

func TestManagerCreateThreadRejectsProviderOwnershipMismatch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		workspace string
		provider  string
	}{
		{"different workspace", "workspace-other", "codex"},
		{"different provider", "workspace-owner", "other-agent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newManagerStore()
			p := &misboundProvider{created: &Thread{
				ID: "thread-1", WorkspaceID: tc.workspace, Provider: tc.provider, Status: ThreadStatusActive,
			}}
			registry := NewRegistry()
			if err := registry.Register(p); err != nil {
				t.Fatal(err)
			}
			manager, err := NewManager(registry, store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.CreateThread(context.Background(), "codex", CreateThreadRequest{WorkspaceID: "workspace-owner"}); err == nil {
				t.Fatal("expected provider ownership mismatch to fail closed")
			}
			if len(store.threads) != 0 {
				t.Fatal("misbound thread must never be persisted")
			}
		})
	}
}

func TestManagerResumeThreadRejectsProviderIdentityMismatch(t *testing.T) {
	for _, tc := range []struct {
		name           string
		workspace      string
		provider       string
		providerThread string
	}{
		{"different workspace", "workspace-other", "codex", "native-owner"},
		{"different provider", "workspace-owner", "other-agent", "native-owner"},
		{"different native session", "workspace-owner", "codex", "native-other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newManagerStore()
			original := Thread{
				ID: "thread-1", WorkspaceID: "workspace-owner", Provider: "codex",
				ProviderThreadID: "native-owner", Status: ThreadStatusActive,
			}
			if err := store.PutThread(context.Background(), original); err != nil {
				t.Fatal(err)
			}
			p := &misboundProvider{resumed: &Thread{
				ID: original.ID, WorkspaceID: tc.workspace, Provider: tc.provider,
				ProviderThreadID: tc.providerThread, Status: ThreadStatusActive,
			}}
			registry := NewRegistry()
			if err := registry.Register(p); err != nil {
				t.Fatal(err)
			}
			manager, err := NewManager(registry, store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.ResumeThread(context.Background(), original.ID); err == nil {
				t.Fatal("expected resumed identity mismatch to fail closed")
			}
			got, err := store.GetThread(context.Background(), original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.WorkspaceID != original.WorkspaceID || got.Provider != original.Provider || got.ProviderThreadID != original.ProviderThreadID {
				t.Fatalf("persisted thread identity changed: %#v", got)
			}
		})
	}
}

func TestManagerRunTurnRejectsForeignNativeSession(t *testing.T) {
	store := newManagerStore()
	if err := store.PutThread(context.Background(), Thread{
		ID: "thread-1", WorkspaceID: "workspace-owner", Provider: "codex",
		ProviderThreadID: "native-owner", Status: ThreadStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	p := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatal(err)
	}

	_, err = manager.RunTurn(context.Background(), RunTurnRequest{
		ThreadID: "thread-1", ProviderThreadID: "native-other",
		Input: TurnInput{Text: "run task"},
	})
	if err == nil {
		t.Fatal("foreign provider session must be rejected")
	}
	if p.lastRun.ThreadID != "" {
		t.Fatal("provider must not receive unauthorized turn")
	}

	_, err = manager.RunTurn(context.Background(), RunTurnRequest{
		ThreadID: "thread-1", Input: TurnInput{Text: "run task"},
	})
	if err != nil {
		t.Fatalf("valid turn should be admitted: %v", err)
	}
	if p.lastRun.ProviderThreadID != "native-owner" {
		t.Fatalf("provider native session = %q, want native-owner", p.lastRun.ProviderThreadID)
	}
}

func TestManagerRunTurnRejectsEmptyInputBeforeProviderInvocation(t *testing.T) {
	store := newManagerStore()
	if err := store.PutThread(context.Background(), Thread{
		ID: "thread-1", WorkspaceID: "workspace-owner", Provider: "codex",
		ProviderThreadID: "native-owner", Status: ThreadStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	p := &managerProvider{}
	registry := NewRegistry()
	if err := registry.Register(p); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(registry, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RunTurn(context.Background(), RunTurnRequest{ThreadID: "thread-1"}); err == nil {
		t.Fatal("empty input must be rejected")
	}
	if p.lastRun.ThreadID != "" {
		t.Fatal("provider must not receive invalid turn")
	}
}
