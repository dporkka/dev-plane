package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentruntime "github.com/ai-dev-control-plane/agent-runtime"
)

const (
	defaultApprovalPolicy = "on-request"
	defaultSandbox        = "read-only"
)

// Notification is a raw Codex app-server notification.
type Notification struct {
	Method string
	Params json.RawMessage
}

// RPCClient is the transport contract used by the Codex provider.
// StdioClient is the production implementation; tests can use an in-memory fake.
type RPCClient interface {
	Call(ctx context.Context, method string, params any, result any) error
	Notify(ctx context.Context, method string, params any) error
	Subscribe() (<-chan Notification, func())
	Close() error
}

// WorkspaceDirResolver resolves the environment-owned filesystem path for a workspace.
type WorkspaceDirResolver func(ctx context.Context, workspaceID string) (string, error)

// Config configures provider-level Codex behavior.
type Config struct {
	ApprovalPolicy      string
	Sandbox             string
	ResolveWorkspaceDir WorkspaceDirResolver
}

// Provider adapts Codex app-server to the provider-neutral agent runtime contract.
type Provider struct {
	client RPCClient
	config Config
}

// NewProvider creates a Codex provider around an initialized RPC client.
func NewProvider(client RPCClient, config Config) *Provider {
	if strings.TrimSpace(config.ApprovalPolicy) == "" {
		config.ApprovalPolicy = defaultApprovalPolicy
	}
	if strings.TrimSpace(config.Sandbox) == "" {
		config.Sandbox = defaultSandbox
	}
	return &Provider{client: client, config: config}
}

func (p *Provider) Name() string { return "codex" }

func (p *Provider) Capabilities() agentruntime.CapabilitySet {
	return agentruntime.NewCapabilitySet(
		agentruntime.CapabilityResumeThread,
		agentruntime.CapabilityInterruptTurn,
		agentruntime.CapabilitySteerTurn,
		agentruntime.CapabilityModelSwitch,
		agentruntime.CapabilityCompact,
		agentruntime.CapabilityStructuredOutput,
	)
}

// Close closes the underlying Codex connection when the provider owns it.
func (p *Provider) Close() error {
	if p == nil || p.client == nil {
		return nil
	}
	return p.client.Close()
}

func (p *Provider) CreateThread(ctx context.Context, req agentruntime.CreateThreadRequest) (*agentruntime.Thread, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("codex client is not configured")
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate create thread: %w", err)
	}

	params := map[string]any{
		"approvalPolicy": p.config.ApprovalPolicy,
		"sandbox":        p.config.Sandbox,
	}
	if req.Model != "" {
		params["model"] = req.Model
	}
	if req.Instructions != "" {
		params["developerInstructions"] = req.Instructions
	}
	if p.config.ResolveWorkspaceDir != nil {
		cwd, err := p.config.ResolveWorkspaceDir(ctx, req.WorkspaceID)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace %s: %w", req.WorkspaceID, err)
		}
		if strings.TrimSpace(cwd) != "" {
			params["cwd"] = cwd
		}
	}

	var response threadResponse
	if err := p.client.Call(ctx, "thread/start", params, &response); err != nil {
		return nil, fmt.Errorf("codex thread/start: %w", err)
	}
	return mapThread(response, req.WorkspaceID, response.Thread.ID, req.Metadata, req.Model), nil
}

func (p *Provider) ResumeThread(ctx context.Context, req agentruntime.ResumeThreadRequest) (*agentruntime.Thread, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("codex client is not configured")
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate resume thread: %w", err)
	}

	providerThreadID := firstNonEmpty(req.ProviderThreadID, req.ThreadID)
	params := map[string]any{
		"threadId":     providerThreadID,
		"excludeTurns": true,
	}
	if req.Model != "" {
		params["model"] = req.Model
	}

	var response threadResponse
	if err := p.client.Call(ctx, "thread/resume", params, &response); err != nil {
		return nil, fmt.Errorf("codex thread/resume: %w", err)
	}
	thread := mapThread(response, req.WorkspaceID, req.ThreadID, nil, req.Model)
	if thread.ProviderThreadID == "" {
		thread.ProviderThreadID = providerThreadID
	}
	return thread, nil
}

func (p *Provider) RunTurn(ctx context.Context, req agentruntime.RunTurnRequest) (<-chan agentruntime.Event, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("codex client is not configured")
	}
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate run turn: %w", err)
	}

	input, err := normalizeInput(req.Input)
	if err != nil {
		return nil, err
	}
	providerThreadID := firstNonEmpty(req.ProviderThreadID, req.ThreadID)
	params := map[string]any{
		"threadId": providerThreadID,
		"input":    input,
	}
	if req.Model != "" {
		params["model"] = req.Model
	}
	if len(req.OutputSchema) > 0 {
		var schema any
		if err := json.Unmarshal(req.OutputSchema, &schema); err != nil {
			return nil, fmt.Errorf("decode output schema: %w", err)
		}
		params["outputSchema"] = schema
	}

	notifications, cancel := p.client.Subscribe()
	var response turnStartResponse
	if err := p.client.Call(ctx, "turn/start", params, &response); err != nil {
		cancel()
		return nil, fmt.Errorf("codex turn/start: %w", err)
	}
	if strings.TrimSpace(response.Turn.ID) == "" {
		cancel()
		return nil, fmt.Errorf("codex turn/start returned empty turn id")
	}

	events := make(chan agentruntime.Event, 32)
	go p.streamTurn(ctx, cancel, notifications, req.ThreadID, providerThreadID, response.Turn, events)
	return events, nil
}

func (p *Provider) InterruptTurn(ctx context.Context, req agentruntime.InterruptTurnRequest) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("codex client is not configured")
	}
	threadID := firstNonEmpty(req.ProviderThreadID, req.ThreadID)
	turnID := firstNonEmpty(req.ProviderTurnID, req.TurnID)
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return fmt.Errorf("thread id and turn id are required")
	}
	var response map[string]any
	if err := p.client.Call(ctx, "turn/interrupt", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
	}, &response); err != nil {
		return fmt.Errorf("codex turn/interrupt: %w", err)
	}
	return nil
}

func (p *Provider) SteerTurn(ctx context.Context, req agentruntime.SteerTurnRequest) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("codex client is not configured")
	}
	threadID := firstNonEmpty(req.ProviderThreadID, req.ThreadID)
	turnID := firstNonEmpty(req.ProviderTurnID, req.TurnID)
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return fmt.Errorf("thread id and turn id are required")
	}
	input, err := normalizeInput(req.Input)
	if err != nil {
		return err
	}
	var response map[string]any
	if err := p.client.Call(ctx, "turn/steer", map[string]any{
		"threadId":       threadID,
		"expectedTurnId": turnID,
		"input":          input,
	}, &response); err != nil {
		return fmt.Errorf("codex turn/steer: %w", err)
	}
	return nil
}

func (p *Provider) CompactThread(ctx context.Context, threadID string) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("codex client is not configured")
	}
	if strings.TrimSpace(threadID) == "" {
		return fmt.Errorf("thread id is required")
	}
	var response map[string]any
	if err := p.client.Call(ctx, "thread/compact/start", map[string]any{
		"threadId": threadID,
	}, &response); err != nil {
		return fmt.Errorf("codex thread/compact/start: %w", err)
	}
	return nil
}

func (p *Provider) streamTurn(
	ctx context.Context,
	cancel func(),
	notifications <-chan Notification,
	threadID string,
	providerThreadID string,
	started codexTurn,
	out chan<- agentruntime.Event,
) {
	defer close(out)
	defer cancel()

	sequence := int64(1)
	out <- agentruntime.Event{
		Sequence:   sequence,
		Type:       agentruntime.EventTypeTurnStarted,
		ThreadID:   threadID,
		TurnID:     started.ID,
		Status:     agentruntime.TurnStatusRunning,
		OccurredAt: unixSeconds(started.StartedAt, time.Now().UTC()),
	}
	sequence++

	for {
		select {
		case <-ctx.Done():
			return
		case notification, ok := <-notifications:
			if !ok {
				return
			}
			event, terminal, matches, err := mapNotification(notification, threadID, providerThreadID, started.ID)
			if err != nil || !matches {
				continue
			}
			event.Sequence = sequence
			sequence++
			out <- event
			if terminal {
				return
			}
		}
	}
}

func mapNotification(
	notification Notification,
	threadID string,
	providerThreadID string,
	turnID string,
) (agentruntime.Event, bool, bool, error) {
	switch notification.Method {
	case "item/started", "item/completed":
		var payload itemNotification
		if err := json.Unmarshal(notification.Params, &payload); err != nil {
			return agentruntime.Event{}, false, false, err
		}
		if payload.ThreadID != providerThreadID || payload.TurnID != turnID {
			return agentruntime.Event{}, false, false, nil
		}
		status := agentruntime.ItemStatusRunning
		eventType := agentruntime.EventTypeItemStarted
		occurredAt := unixMillis(payload.StartedAtMS, time.Now().UTC())
		if notification.Method == "item/completed" {
			status = agentruntime.ItemStatusCompleted
			eventType = agentruntime.EventTypeItemCompleted
			occurredAt = unixMillis(payload.CompletedAtMS, time.Now().UTC())
		}
		item, err := mapItem(payload.Item, threadID, turnID, status, occurredAt)
		if err != nil {
			return agentruntime.Event{}, false, false, err
		}
		return agentruntime.Event{
			Type:       eventType,
			ThreadID:   threadID,
			TurnID:     turnID,
			Status:     agentruntime.TurnStatusRunning,
			Item:       &item,
			OccurredAt: occurredAt,
		}, false, true, nil

	case "turn/completed":
		var payload turnNotification
		if err := json.Unmarshal(notification.Params, &payload); err != nil {
			return agentruntime.Event{}, false, false, err
		}
		if payload.ThreadID != providerThreadID || payload.Turn.ID != turnID {
			return agentruntime.Event{}, false, false, nil
		}
		status := mapTurnStatus(payload.Turn.Status)
		eventType := agentruntime.EventTypeTurnCompleted
		if status == agentruntime.TurnStatusFailed {
			eventType = agentruntime.EventTypeTurnFailed
		} else if status == agentruntime.TurnStatusInterrupted {
			eventType = agentruntime.EventTypeTurnStatus
		}
		return agentruntime.Event{
			Type:       eventType,
			ThreadID:   threadID,
			TurnID:     turnID,
			Status:     status,
			Error:      payload.Turn.Error.Message,
			OccurredAt: unixSeconds(payload.Turn.CompletedAt, time.Now().UTC()),
		}, true, true, nil
	default:
		return agentruntime.Event{}, false, false, nil
	}
}

func normalizeInput(input agentruntime.TurnInput) ([]any, error) {
	var result []any
	if strings.TrimSpace(input.Text) != "" {
		result = append(result, map[string]any{
			"type":          "text",
			"text":          input.Text,
			"text_elements": []any{},
		})
	}
	if len(input.Data) > 0 {
		var values []any
		if err := json.Unmarshal(input.Data, &values); err != nil {
			return nil, fmt.Errorf("turn input data must be a JSON array: %w", err)
		}
		result = append(result, values...)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("turn input is required")
	}
	return result, nil
}

func mapThread(response threadResponse, workspaceID, threadID string, metadata json.RawMessage, requestedModel string) *agentruntime.Thread {
	providerThreadID := response.Thread.ID
	if strings.TrimSpace(threadID) == "" {
		threadID = providerThreadID
	}
	model := firstNonEmpty(response.Model, response.Thread.Model, requestedModel)
	createdAt := unixSeconds(response.Thread.CreatedAt, time.Time{})
	updatedAt := unixSeconds(response.Thread.UpdatedAt, createdAt)
	return &agentruntime.Thread{
		ID:               threadID,
		WorkspaceID:      workspaceID,
		Provider:         "codex",
		ProviderThreadID: providerThreadID,
		Model:            model,
		Status:           agentruntime.ThreadStatusActive,
		Metadata:         metadata,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}
}

func mapItem(raw json.RawMessage, threadID, turnID string, status agentruntime.ItemStatus, occurredAt time.Time) (agentruntime.Item, error) {
	var header struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return agentruntime.Item{}, fmt.Errorf("decode codex item: %w", err)
	}
	if strings.TrimSpace(header.ID) == "" || strings.TrimSpace(header.Type) == "" {
		return agentruntime.Item{}, fmt.Errorf("codex item is missing id or type")
	}

	role := ""
	switch header.Type {
	case "userMessage":
		role = "user"
	case "agentMessage":
		role = "assistant"
	}

	return agentruntime.Item{
		ID:             header.ID,
		ThreadID:       threadID,
		TurnID:         turnID,
		ProviderItemID: header.ID,
		Type:           mapItemType(header.Type),
		Status:         status,
		Role:           role,
		Name:           header.Type,
		Payload:        append(json.RawMessage(nil), raw...),
		CreatedAt:      occurredAt,
		UpdatedAt:      occurredAt,
	}, nil
}

func mapItemType(itemType string) agentruntime.ItemType {
	switch itemType {
	case "userMessage", "hookPrompt", "agentMessage", "plan", "reasoning":
		return agentruntime.ItemTypeMessage
	case "commandExecution":
		return agentruntime.ItemTypeCommand
	case "fileChange":
		return agentruntime.ItemTypeFileChange
	case "functionCallOutput", "mcpToolCall", "dynamicToolCall", "collabAgentToolCall":
		return agentruntime.ItemTypeToolCall
	case "imageView", "imageGeneration", "webSearch", "subAgentActivity", "contextCompaction", "enteredReviewMode", "exitedReviewMode", "sleep":
		return agentruntime.ItemTypeArtifact
	default:
		return agentruntime.ItemTypeArtifact
	}
}

func mapTurnStatus(status string) agentruntime.TurnStatus {
	switch status {
	case "completed":
		return agentruntime.TurnStatusCompleted
	case "interrupted":
		return agentruntime.TurnStatusInterrupted
	case "failed":
		return agentruntime.TurnStatusFailed
	case "inProgress":
		return agentruntime.TurnStatusRunning
	default:
		return agentruntime.TurnStatusFailed
	}
}

func unixSeconds(value *int64, fallback time.Time) time.Time {
	if value == nil || *value <= 0 {
		return fallback
	}
	return time.Unix(*value, 0).UTC()
}

func unixMillis(value int64, fallback time.Time) time.Time {
	if value <= 0 {
		return fallback
	}
	return time.UnixMilli(value).UTC()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type threadResponse struct {
	Thread codexThread `json:"thread"`
	Model  string      `json:"model"`
}

type codexThread struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	CreatedAt *int64 `json:"createdAt"`
	UpdatedAt *int64 `json:"updatedAt"`
}

type turnStartResponse struct {
	Turn codexTurn `json:"turn"`
}

type turnNotification struct {
	ThreadID string    `json:"threadId"`
	Turn     codexTurn `json:"turn"`
}

type codexTurn struct {
	ID          string     `json:"id"`
	Status      string     `json:"status"`
	Error       codexError `json:"error"`
	StartedAt   *int64     `json:"startedAt"`
	CompletedAt *int64     `json:"completedAt"`
}

type codexError struct {
	Message string `json:"message"`
}

type itemNotification struct {
	ThreadID      string          `json:"threadId"`
	TurnID        string          `json:"turnId"`
	Item          json.RawMessage `json:"item"`
	StartedAtMS   int64           `json:"startedAtMs"`
	CompletedAtMS int64           `json:"completedAtMs"`
}

var (
	_ agentruntime.Provider           = (*Provider)(nil)
	_ agentruntime.ResumeProvider     = (*Provider)(nil)
	_ agentruntime.InterruptProvider  = (*Provider)(nil)
	_ agentruntime.SteeringProvider   = (*Provider)(nil)
	_ agentruntime.CompactionProvider = (*Provider)(nil)
)
