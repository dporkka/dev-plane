package omp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

// WorkspaceDirResolver resolves the environment-owned filesystem path for a workspace.
type WorkspaceDirResolver func(ctx context.Context, workspaceID string) (string, error)

// Config controls OMP process/session creation.
type Config struct {
	SessionRoot          string
	Binary               string
	Args                 []string
	Env                  map[string]string
	DefaultModelProvider string
	ResolveWorkspaceDir  WorkspaceDirResolver
	NewClient            ClientFactory
}

// Provider adapts OMP RPC sessions to Dev Plane's provider-neutral runtime.
// Each durable thread owns an isolated OMP process/session binding so turns from
// different workspaces cannot interfere through process-global session state.
type Provider struct {
	config      Config
	sessionRoot string
	newClient   ClientFactory

	mu      sync.Mutex
	clients map[string]RPCClient
	active  map[string]bool
}

// NewProvider creates an OMP runtime provider. SessionRoot is required because
// provider-native resume handles are filesystem-backed and must stay inside a
// control-plane-owned directory.
func NewProvider(config Config) (*Provider, error) {
	if strings.TrimSpace(config.SessionRoot) == "" {
		return nil, errors.New("omp session root is required")
	}
	root, err := filepath.Abs(config.SessionRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve omp session root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create omp session root: %w", err)
	}
	factory := config.NewClient
	if factory == nil {
		factory = func(ctx context.Context, cfg StdioConfig) (RPCClient, error) {
			return NewStdioClient(ctx, cfg)
		}
	}
	return &Provider{
		config:      config,
		sessionRoot: root,
		newClient:   factory,
		clients:     make(map[string]RPCClient),
		active:      make(map[string]bool),
	}, nil
}

func (p *Provider) Name() string { return "omp" }

func (p *Provider) Capabilities() agentruntime.CapabilitySet {
	return agentruntime.NewCapabilitySet(
		agentruntime.CapabilityResumeThread,
		agentruntime.CapabilityInterruptTurn,
		agentruntime.CapabilitySteerTurn,
		agentruntime.CapabilityModelSwitch,
		agentruntime.CapabilityCompact,
	)
}

func (p *Provider) CreateThread(ctx context.Context, req agentruntime.CreateThreadRequest) (*agentruntime.Thread, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate create thread: %w", err)
	}

	token, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("allocate omp thread id: %w", err)
	}
	sessionDir := filepath.Join(p.sessionRoot, "thread-"+token)
	if err := os.Mkdir(sessionDir, 0o700); err != nil {
		return nil, fmt.Errorf("create omp thread directory: %w", err)
	}

	client, err := p.startClient(ctx, req.WorkspaceID, req.Instructions)
	if err != nil {
		_ = os.RemoveAll(sessionDir)
		return nil, err
	}
	binding, err := client.OpenSession(ctx, sessionDir)
	if err != nil {
		_ = client.Close()
		_ = os.RemoveAll(sessionDir)
		return nil, err
	}
	if req.Model != "" {
		provider, modelID, err := parseModelRef(req.Model, p.config.DefaultModelProvider)
		if err != nil {
			_ = client.Close()
			return nil, err
		}
		if err := client.SetModel(ctx, provider, modelID); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

	now := time.Now().UTC()
	threadID := binding.SessionID
	if strings.TrimSpace(threadID) == "" {
		threadID = "omp-" + token
	}
	thread := &agentruntime.Thread{
		ID:               threadID,
		WorkspaceID:      req.WorkspaceID,
		Provider:         p.Name(),
		ProviderThreadID: sessionDir,
		Model:            req.Model,
		Status:           agentruntime.ThreadStatusActive,
		Metadata:         append(json.RawMessage(nil), req.Metadata...),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	p.registerClient(sessionDir, thread.ID, client)
	return thread, nil
}

func (p *Provider) ResumeThread(ctx context.Context, req agentruntime.ResumeThreadRequest) (*agentruntime.Thread, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate resume thread: %w", err)
	}
	sessionDir, err := p.validateSessionDir(req.ProviderThreadID)
	if err != nil {
		return nil, err
	}
	if client := p.lookupClient(sessionDir, req.ThreadID); client != nil {
		return &agentruntime.Thread{
			ID:               req.ThreadID,
			WorkspaceID:      req.WorkspaceID,
			Provider:         p.Name(),
			ProviderThreadID: sessionDir,
			Model:            req.Model,
			Status:           agentruntime.ThreadStatusActive,
			UpdatedAt:        time.Now().UTC(),
		}, nil
	}

	client, err := p.startClient(ctx, req.WorkspaceID, "")
	if err != nil {
		return nil, err
	}
	binding, err := client.OpenSession(ctx, sessionDir)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	if req.Model != "" {
		provider, modelID, err := parseModelRef(req.Model, p.config.DefaultModelProvider)
		if err != nil {
			_ = client.Close()
			return nil, err
		}
		if err := client.SetModel(ctx, provider, modelID); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	threadID := req.ThreadID
	if strings.TrimSpace(threadID) == "" {
		threadID = binding.SessionID
	}
	p.registerClient(sessionDir, threadID, client)
	return &agentruntime.Thread{
		ID:               threadID,
		WorkspaceID:      req.WorkspaceID,
		Provider:         p.Name(),
		ProviderThreadID: sessionDir,
		Model:            req.Model,
		Status:           agentruntime.ThreadStatusActive,
		UpdatedAt:        time.Now().UTC(),
	}, nil
}

func (p *Provider) RunTurn(ctx context.Context, req agentruntime.RunTurnRequest) (<-chan agentruntime.Event, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate run turn: %w", err)
	}
	message, err := normalizePrompt(req.Input)
	if err != nil {
		return nil, err
	}
	handle := firstNonEmpty(req.ProviderThreadID, req.ThreadID)
	client := p.lookupClient(handle, req.ThreadID)
	if client == nil {
		return nil, fmt.Errorf("omp thread %q is not attached; resume it before running a turn", req.ThreadID)
	}
	if err := p.markActive(handle); err != nil {
		return nil, err
	}
	if req.Model != "" {
		provider, modelID, err := parseModelRef(req.Model, p.config.DefaultModelProvider)
		if err != nil {
			p.clearActive(handle)
			return nil, err
		}
		if err := client.SetModel(ctx, provider, modelID); err != nil {
			p.clearActive(handle)
			return nil, err
		}
	}

	frames, cancel := client.Subscribe()
	promptID, err := client.Prompt(ctx, message)
	if err != nil {
		cancel()
		p.clearActive(handle)
		return nil, err
	}
	if strings.TrimSpace(promptID) == "" {
		cancel()
		p.clearActive(handle)
		return nil, errors.New("omp prompt returned empty request id")
	}

	events := make(chan agentruntime.Event, 32)
	go p.streamTurn(ctx, handle, cancel, frames, req.ThreadID, promptID, events)
	return events, nil
}

func (p *Provider) InterruptTurn(ctx context.Context, req agentruntime.InterruptTurnRequest) error {
	handle := firstNonEmpty(req.ProviderThreadID, req.ThreadID)
	client := p.lookupClient(handle, req.ThreadID)
	if client == nil {
		return fmt.Errorf("omp thread %q is not attached", req.ThreadID)
	}
	return client.Abort(ctx)
}

func (p *Provider) SteerTurn(ctx context.Context, req agentruntime.SteerTurnRequest) error {
	if err := req.Input.Validate(); err != nil {
		return err
	}
	message, err := normalizePrompt(req.Input)
	if err != nil {
		return err
	}
	handle := firstNonEmpty(req.ProviderThreadID, req.ThreadID)
	client := p.lookupClient(handle, req.ThreadID)
	if client == nil {
		return fmt.Errorf("omp thread %q is not attached", req.ThreadID)
	}
	return client.Steer(ctx, message)
}

func (p *Provider) CompactThread(ctx context.Context, threadID string) error {
	client := p.lookupClient(threadID)
	if client == nil {
		return fmt.Errorf("omp thread %q is not attached", threadID)
	}
	return client.Compact(ctx)
}

func (p *Provider) streamTurn(
	ctx context.Context,
	handle string,
	cancel func(),
	frames <-chan Frame,
	threadID string,
	turnID string,
	events chan<- agentruntime.Event,
) {
	defer cancel()
	defer p.clearActive(handle)
	defer close(events)

	sequence := int64(1)
	if !sendEvent(ctx, events, agentruntime.Event{
		Sequence:       sequence,
		Type:           agentruntime.EventTypeTurnStarted,
		ThreadID:       threadID,
		TurnID:         turnID,
		ProviderTurnID: turnID,
		Status:         agentruntime.TurnStatusRunning,
		OccurredAt:     time.Now().UTC(),
	}) {
		return
	}
	sequence++

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-frames:
			if !ok {
				return
			}
			event, mapped, terminal := mapFrame(frame, threadID, turnID, sequence)
			if !mapped {
				continue
			}
			sequence++
			if !sendEvent(ctx, events, event) {
				return
			}
			if terminal {
				return
			}
		}
	}
}

func mapFrame(frame Frame, threadID, turnID string, sequence int64) (agentruntime.Event, bool, bool) {
	now := time.Now().UTC()
	base := agentruntime.Event{
		Sequence:       sequence,
		ThreadID:       threadID,
		TurnID:         turnID,
		ProviderTurnID: turnID,
		OccurredAt:     now,
	}

	switch frame.Type {
	case "message_start", "message_end":
		if strings.TrimSpace(frame.MessageID) == "" {
			return agentruntime.Event{}, false, false
		}
		status := agentruntime.ItemStatusRunning
		eventType := agentruntime.EventTypeItemStarted
		if frame.Type == "message_end" {
			status = agentruntime.ItemStatusCompleted
			eventType = agentruntime.EventTypeItemCompleted
		}
		base.Type = eventType
		base.Item = &agentruntime.Item{
			ID:             frame.MessageID,
			ThreadID:       threadID,
			TurnID:         turnID,
			ProviderItemID: frame.MessageID,
			Type:           agentruntime.ItemTypeMessage,
			Status:         status,
			Role:           messageRole(frame.Raw),
			Payload:        append(json.RawMessage(nil), frame.Raw...),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		return base, true, false

	case "tool_execution_start", "tool_execution_end":
		if strings.TrimSpace(frame.ToolCallID) == "" {
			return agentruntime.Event{}, false, false
		}
		status := agentruntime.ItemStatusRunning
		eventType := agentruntime.EventTypeItemStarted
		if frame.Type == "tool_execution_end" {
			status = agentruntime.ItemStatusCompleted
			eventType = agentruntime.EventTypeItemCompleted
			if frame.IsError {
				status = agentruntime.ItemStatusFailed
				eventType = agentruntime.EventTypeItemFailed
			}
		}
		base.Type = eventType
		base.Item = &agentruntime.Item{
			ID:             frame.ToolCallID,
			ThreadID:       threadID,
			TurnID:         turnID,
			ProviderItemID: frame.ToolCallID,
			Type:           agentruntime.ItemTypeToolCall,
			Status:         status,
			Name:           frame.ToolName,
			Payload:        append(json.RawMessage(nil), frame.Raw...),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		return base, true, false

	case "prompt_result":
		if frame.ID != turnID {
			return agentruntime.Event{}, false, false
		}
		switch frame.Status {
		case "completed":
			base.Type = agentruntime.EventTypeTurnCompleted
			base.Status = agentruntime.TurnStatusCompleted
			return base, true, true
		case "aborted":
			base.Type = agentruntime.EventTypeTurnStatus
			base.Status = agentruntime.TurnStatusInterrupted
			return base, true, true
		case "error":
			base.Type = agentruntime.EventTypeTurnFailed
			base.Status = agentruntime.TurnStatusFailed
			if frame.PromptError != nil {
				base.Error = frame.PromptError.Message
			}
			if strings.TrimSpace(base.Error) == "" {
				base.Error = "OMP prompt failed"
			}
			return base, true, true
		default:
			return agentruntime.Event{}, false, false
		}
	default:
		return agentruntime.Event{}, false, false
	}
}

func (p *Provider) startClient(ctx context.Context, workspaceID, instructions string) (RPCClient, error) {
	cwd := ""
	if p.config.ResolveWorkspaceDir != nil {
		resolved, err := p.config.ResolveWorkspaceDir(ctx, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace %s: %w", workspaceID, err)
		}
		cwd = resolved
	}
	client, err := p.newClient(ctx, StdioConfig{
		Binary:             p.config.Binary,
		Args:               append([]string(nil), p.config.Args...),
		Cwd:                cwd,
		Env:                cloneStrings(p.config.Env),
		AppendSystemPrompt: instructions,
	})
	if err != nil {
		return nil, fmt.Errorf("start omp rpc client: %w", err)
	}
	return client, nil
}

func (p *Provider) validateSessionDir(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("omp provider thread id is required")
	}
	candidate, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve omp session directory: %w", err)
	}
	rel, err := filepath.Rel(p.sessionRoot, candidate)
	if err != nil {
		return "", fmt.Errorf("compare omp session directory: %w", err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("omp session directory %q escapes configured root", value)
	}
	return candidate, nil
}

func (p *Provider) registerClient(sessionDir, threadID string, client RPCClient) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[sessionDir] = client
	if strings.TrimSpace(threadID) != "" {
		p.clients[threadID] = client
	}
}

func (p *Provider) lookupClient(handles ...string) RPCClient {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, handle := range handles {
		if client := p.clients[handle]; client != nil {
			return client
		}
	}
	return nil
}

func (p *Provider) markActive(handle string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active[handle] {
		return fmt.Errorf("omp thread %q already has an active turn", handle)
	}
	p.active[handle] = true
	return nil
}

func (p *Provider) clearActive(handle string) {
	p.mu.Lock()
	delete(p.active, handle)
	p.mu.Unlock()
}

func normalizePrompt(input agentruntime.TurnInput) (string, error) {
	if strings.TrimSpace(input.Text) != "" {
		return input.Text, nil
	}
	if len(input.Data) > 0 {
		var text string
		if err := json.Unmarshal(input.Data, &text); err == nil && strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	return "", errors.New("OMP currently requires textual turn input")
}

func parseModelRef(value, defaultProvider string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", errors.New("OMP model is required")
	}
	if provider, modelID, ok := strings.Cut(value, "/"); ok {
		provider = strings.TrimSpace(provider)
		modelID = strings.TrimSpace(modelID)
		if provider == "" || modelID == "" {
			return "", "", fmt.Errorf("invalid OMP model reference %q", value)
		}
		return provider, modelID, nil
	}
	defaultProvider = strings.TrimSpace(defaultProvider)
	if defaultProvider == "" {
		return "", "", fmt.Errorf("OMP model %q has no provider prefix and no default provider is configured", value)
	}
	return defaultProvider, value, nil
}

func messageRole(raw json.RawMessage) string {
	var payload struct {
		Message struct {
			Role string `json:"role"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return payload.Message.Role
}

func randomToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func sendEvent(ctx context.Context, events chan<- agentruntime.Event, event agentruntime.Event) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- event:
		return true
	}
}

func cloneStrings(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var (
	_ agentruntime.Provider          = (*Provider)(nil)
	_ agentruntime.ResumeProvider    = (*Provider)(nil)
	_ agentruntime.InterruptProvider = (*Provider)(nil)
	_ agentruntime.SteeringProvider  = (*Provider)(nil)
	_ agentruntime.CompactionProvider = (*Provider)(nil)
)
