package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Capability identifies optional behavior exposed by a coding-agent provider.
type Capability uint8

const (
	CapabilityResumeThread Capability = iota + 1
	CapabilityInterruptTurn
	CapabilitySteerTurn
	CapabilityPromptlessContinuation
	CapabilityModelSwitch
	CapabilityRollback
	CapabilityCompact
	CapabilityApprovalRequests
	CapabilityQuestions
	CapabilityStructuredOutput
)

// String returns the stable wire name for a capability.
func (c Capability) String() string {
	switch c {
	case CapabilityResumeThread:
		return "resume_thread"
	case CapabilityInterruptTurn:
		return "interrupt_turn"
	case CapabilitySteerTurn:
		return "steer_turn"
	case CapabilityPromptlessContinuation:
		return "promptless_continuation"
	case CapabilityModelSwitch:
		return "model_switch"
	case CapabilityRollback:
		return "rollback"
	case CapabilityCompact:
		return "compact"
	case CapabilityApprovalRequests:
		return "approval_requests"
	case CapabilityQuestions:
		return "questions"
	case CapabilityStructuredOutput:
		return "structured_output"
	default:
		return "unknown"
	}
}

// CapabilitySet is a compact, immutable-by-value set of provider capabilities.
type CapabilitySet uint64

// NewCapabilitySet constructs a capability set.
func NewCapabilitySet(capabilities ...Capability) CapabilitySet {
	var set CapabilitySet
	for _, capability := range capabilities {
		if capability == 0 || capability > 64 {
			continue
		}
		set |= CapabilitySet(1) << (capability - 1)
	}
	return set
}

// Supports reports whether the capability is present.
func (s CapabilitySet) Supports(capability Capability) bool {
	if capability == 0 || capability > 64 {
		return false
	}
	return s&(CapabilitySet(1)<<(capability-1)) != 0
}

// List returns the supported capabilities in stable declaration order.
func (s CapabilitySet) List() []Capability {
	all := []Capability{
		CapabilityResumeThread,
		CapabilityInterruptTurn,
		CapabilitySteerTurn,
		CapabilityPromptlessContinuation,
		CapabilityModelSwitch,
		CapabilityRollback,
		CapabilityCompact,
		CapabilityApprovalRequests,
		CapabilityQuestions,
		CapabilityStructuredOutput,
	}
	result := make([]Capability, 0, len(all))
	for _, capability := range all {
		if s.Supports(capability) {
			result = append(result, capability)
		}
	}
	return result
}

// ThreadStatus describes the lifecycle of a provider-neutral conversation.
type ThreadStatus string

const (
	ThreadStatusActive   ThreadStatus = "active"
	ThreadStatusArchived ThreadStatus = "archived"
	ThreadStatusError    ThreadStatus = "error"
)

// TurnStatus describes the durable lifecycle of one agent execution turn.
type TurnStatus string

const (
	TurnStatusQueued         TurnStatus = "queued"
	TurnStatusRunning        TurnStatus = "running"
	TurnStatusPausedApproval TurnStatus = "paused_approval"
	TurnStatusPausedInput    TurnStatus = "paused_input"
	TurnStatusInterrupted    TurnStatus = "interrupted"
	TurnStatusRecovering     TurnStatus = "recovering"
	TurnStatusCompleted      TurnStatus = "completed"
	TurnStatusFailed         TurnStatus = "failed"
)

// ItemType identifies one observable unit emitted during a turn.
type ItemType string

const (
	ItemTypeMessage    ItemType = "message"
	ItemTypeToolCall   ItemType = "tool_call"
	ItemTypeCommand    ItemType = "command"
	ItemTypeFileChange ItemType = "file_change"
	ItemTypeApproval   ItemType = "approval"
	ItemTypeQuestion   ItemType = "question"
	ItemTypeArtifact   ItemType = "artifact"
	ItemTypeError      ItemType = "error"
)

// ItemStatus describes the lifecycle of a turn item.
type ItemStatus string

const (
	ItemStatusPending   ItemStatus = "pending"
	ItemStatusRunning   ItemStatus = "running"
	ItemStatusCompleted ItemStatus = "completed"
	ItemStatusFailed    ItemStatus = "failed"
	ItemStatusCancelled ItemStatus = "cancelled"
)

// Thread is the durable provider-neutral conversation container.
type Thread struct {
	ID               string          `json:"id"`
	WorkspaceID      string          `json:"workspace_id"`
	Provider         string          `json:"provider"`
	ProviderThreadID string          `json:"provider_thread_id,omitempty"`
	Model            string          `json:"model,omitempty"`
	Status           ThreadStatus    `json:"status"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	CreatedAt        time.Time       `json:"created_at,omitempty"`
	UpdatedAt        time.Time       `json:"updated_at,omitempty"`
}

// Validate checks the minimum ownership and lifecycle invariants for a thread.
func (t Thread) Validate() error {
	switch {
	case strings.TrimSpace(t.ID) == "":
		return fmt.Errorf("thread id is required")
	case strings.TrimSpace(t.WorkspaceID) == "":
		return fmt.Errorf("thread workspace id is required")
	case strings.TrimSpace(t.Provider) == "":
		return fmt.Errorf("thread provider is required")
	case !validThreadStatus(t.Status):
		return fmt.Errorf("invalid thread status %q", t.Status)
	default:
		return nil
	}
}

// Turn is one resumable execution cycle within a thread.
type Turn struct {
	ID             string          `json:"id"`
	ThreadID       string          `json:"thread_id"`
	ProviderTurnID string          `json:"provider_turn_id,omitempty"`
	Status         TurnStatus      `json:"status"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at,omitempty"`
	UpdatedAt      time.Time       `json:"updated_at,omitempty"`
}

// Validate checks the minimum ownership and lifecycle invariants for a turn.
func (t Turn) Validate() error {
	switch {
	case strings.TrimSpace(t.ID) == "":
		return fmt.Errorf("turn id is required")
	case strings.TrimSpace(t.ThreadID) == "":
		return fmt.Errorf("turn thread id is required")
	case !validTurnStatus(t.Status):
		return fmt.Errorf("invalid turn status %q", t.Status)
	default:
		return nil
	}
}

// Item is an observable unit of work or communication inside a turn.
type Item struct {
	ID             string          `json:"id"`
	ThreadID       string          `json:"thread_id"`
	TurnID         string          `json:"turn_id"`
	ProviderItemID string          `json:"provider_item_id,omitempty"`
	Type           ItemType        `json:"type"`
	Status         ItemStatus      `json:"status"`
	Role           string          `json:"role,omitempty"`
	Name           string          `json:"name,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	CreatedAt      time.Time       `json:"created_at,omitempty"`
	UpdatedAt      time.Time       `json:"updated_at,omitempty"`
}

// Validate checks the minimum ownership and lifecycle invariants for an item.
func (i Item) Validate() error {
	switch {
	case strings.TrimSpace(i.ID) == "":
		return fmt.Errorf("item id is required")
	case strings.TrimSpace(i.ThreadID) == "":
		return fmt.Errorf("item thread id is required")
	case strings.TrimSpace(i.TurnID) == "":
		return fmt.Errorf("item turn id is required")
	case !validItemType(i.Type):
		return fmt.Errorf("invalid item type %q", i.Type)
	case !validItemStatus(i.Status):
		return fmt.Errorf("invalid item status %q", i.Status)
	default:
		return nil
	}
}

// TurnInput is provider-neutral input for starting or steering a turn.
type TurnInput struct {
	Text string          `json:"text,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Validate requires at least one input representation.
func (i TurnInput) Validate() error {
	if strings.TrimSpace(i.Text) == "" && len(i.Data) == 0 {
		return fmt.Errorf("turn input is required")
	}
	return nil
}

// CreateThreadRequest creates a provider session bound to one workspace.
type CreateThreadRequest struct {
	WorkspaceID  string          `json:"workspace_id"`
	Model        string          `json:"model,omitempty"`
	Instructions string          `json:"instructions,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
}

// Validate checks the required create-thread fields.
func (r CreateThreadRequest) Validate() error {
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return fmt.Errorf("workspace id is required")
	}
	return nil
}

// RunTurnRequest starts a new turn on an existing thread.
type RunTurnRequest struct {
	ThreadID         string          `json:"thread_id"`
	ProviderThreadID string          `json:"provider_thread_id,omitempty"`
	Input            TurnInput       `json:"input"`
	Model            string          `json:"model,omitempty"`
	OutputSchema     json.RawMessage `json:"output_schema,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
}

// Validate checks the required run-turn fields.
func (r RunTurnRequest) Validate() error {
	if strings.TrimSpace(r.ThreadID) == "" {
		return fmt.Errorf("thread id is required")
	}
	return r.Input.Validate()
}

// ResumeThreadRequest reattaches a provider-native thread to its durable workspace.
type ResumeThreadRequest struct {
	ThreadID         string `json:"thread_id"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	WorkspaceID      string `json:"workspace_id"`
	Model            string `json:"model,omitempty"`
}

// Validate checks the durable identifiers required to reconstruct a thread.
func (r ResumeThreadRequest) Validate() error {
	if strings.TrimSpace(r.ThreadID) == "" {
		return fmt.Errorf("thread id is required")
	}
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return fmt.Errorf("workspace id is required")
	}
	return nil
}

// InterruptTurnRequest identifies the active turn to interrupt.
type InterruptTurnRequest struct {
	ThreadID         string `json:"thread_id"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	TurnID           string `json:"turn_id"`
	ProviderTurnID   string `json:"provider_turn_id,omitempty"`
}

// SteerTurnRequest supplies additional input to an active turn.
type SteerTurnRequest struct {
	ThreadID         string    `json:"thread_id"`
	ProviderThreadID string    `json:"provider_thread_id,omitempty"`
	TurnID           string    `json:"turn_id"`
	ProviderTurnID   string    `json:"provider_turn_id,omitempty"`
	Input            TurnInput `json:"input"`
}

// ApprovalDecision is the response to a provider approval request.
type ApprovalDecision string

const (
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionDenied   ApprovalDecision = "denied"
)

// ApprovalResponse resumes or rejects a paused approval item.
type ApprovalResponse struct {
	ThreadID string           `json:"thread_id"`
	TurnID   string           `json:"turn_id"`
	ItemID   string           `json:"item_id"`
	Decision ApprovalDecision `json:"decision"`
	Note     string           `json:"note,omitempty"`
}

// QuestionResponse supplies structured or textual user input to a paused turn.
type QuestionResponse struct {
	ThreadID string          `json:"thread_id"`
	TurnID   string          `json:"turn_id"`
	ItemID   string          `json:"item_id"`
	Answer   json.RawMessage `json:"answer"`
}

// RollbackRequest asks a provider to restore conversation state.
type RollbackRequest struct {
	ThreadID     string `json:"thread_id"`
	ToTurnID     string `json:"to_turn_id,omitempty"`
	CheckpointID string `json:"checkpoint_id,omitempty"`
}

// EventType identifies a streamed runtime event.
type EventType string

const (
	EventTypeThreadCreated EventType = "thread_created"
	EventTypeTurnStarted   EventType = "turn_started"
	EventTypeTurnStatus    EventType = "turn_status"
	EventTypeItemStarted   EventType = "item_started"
	EventTypeItemCompleted EventType = "item_completed"
	EventTypeItemFailed    EventType = "item_failed"
	EventTypeTurnCompleted EventType = "turn_completed"
	EventTypeTurnFailed    EventType = "turn_failed"
)

// Event is the ordered provider-neutral stream emitted while a turn executes.
type Event struct {
	Sequence   int64      `json:"sequence"`
	Type       EventType  `json:"type"`
	ThreadID   string     `json:"thread_id"`
	TurnID     string     `json:"turn_id,omitempty"`
	Status     TurnStatus `json:"status,omitempty"`
	Item       *Item      `json:"item,omitempty"`
	Error      string     `json:"error,omitempty"`
	OccurredAt time.Time  `json:"occurred_at"`
}

// Provider is the minimum contract every coding-agent adapter must implement.
// Optional behaviors are expressed through capability-specific interfaces below.
type Provider interface {
	Name() string
	Capabilities() CapabilitySet
	CreateThread(ctx context.Context, req CreateThreadRequest) (*Thread, error)
	RunTurn(ctx context.Context, req RunTurnRequest) (<-chan Event, error)
}

// ResumeProvider resumes a provider-native thread after process or client loss.
type ResumeProvider interface {
	ResumeThread(ctx context.Context, req ResumeThreadRequest) (*Thread, error)
}

// InterruptProvider interrupts an active turn without deleting its thread.
type InterruptProvider interface {
	InterruptTurn(ctx context.Context, req InterruptTurnRequest) error
}

// SteeringProvider injects additional input into an active turn.
type SteeringProvider interface {
	SteerTurn(ctx context.Context, req SteerTurnRequest) error
}

// ApprovalProvider answers a provider-native approval request.
type ApprovalProvider interface {
	RespondApproval(ctx context.Context, response ApprovalResponse) error
}

// QuestionProvider answers a provider-native structured question.
type QuestionProvider interface {
	RespondQuestion(ctx context.Context, response QuestionResponse) error
}

// CompactionProvider compacts provider-native conversation state.
type CompactionProvider interface {
	CompactThread(ctx context.Context, threadID string) error
}

// RollbackProvider restores provider-native conversation state.
type RollbackProvider interface {
	Rollback(ctx context.Context, req RollbackRequest) error
}

// CanTransitionTurnStatus reports whether the durable turn state transition is valid.
func CanTransitionTurnStatus(from, to TurnStatus) bool {
	if from == to {
		return false
	}

	switch from {
	case TurnStatusQueued:
		return to == TurnStatusRunning || to == TurnStatusFailed
	case TurnStatusRunning:
		return to == TurnStatusPausedApproval ||
			to == TurnStatusPausedInput ||
			to == TurnStatusInterrupted ||
			to == TurnStatusRecovering ||
			to == TurnStatusCompleted ||
			to == TurnStatusFailed
	case TurnStatusPausedApproval, TurnStatusPausedInput:
		return to == TurnStatusRunning ||
			to == TurnStatusInterrupted ||
			to == TurnStatusFailed
	case TurnStatusInterrupted:
		return to == TurnStatusRecovering || to == TurnStatusFailed
	case TurnStatusRecovering:
		return to == TurnStatusRunning ||
			to == TurnStatusPausedApproval ||
			to == TurnStatusPausedInput ||
			to == TurnStatusCompleted ||
			to == TurnStatusFailed
	case TurnStatusCompleted, TurnStatusFailed:
		return false
	default:
		return false
	}
}

func validThreadStatus(status ThreadStatus) bool {
	switch status {
	case ThreadStatusActive, ThreadStatusArchived, ThreadStatusError:
		return true
	default:
		return false
	}
}

func validTurnStatus(status TurnStatus) bool {
	switch status {
	case TurnStatusQueued,
		TurnStatusRunning,
		TurnStatusPausedApproval,
		TurnStatusPausedInput,
		TurnStatusInterrupted,
		TurnStatusRecovering,
		TurnStatusCompleted,
		TurnStatusFailed:
		return true
	default:
		return false
	}
}

func validItemType(itemType ItemType) bool {
	switch itemType {
	case ItemTypeMessage,
		ItemTypeToolCall,
		ItemTypeCommand,
		ItemTypeFileChange,
		ItemTypeApproval,
		ItemTypeQuestion,
		ItemTypeArtifact,
		ItemTypeError:
		return true
	default:
		return false
	}
}

func validItemStatus(status ItemStatus) bool {
	switch status {
	case ItemStatusPending,
		ItemStatusRunning,
		ItemStatusCompleted,
		ItemStatusFailed,
		ItemStatusCancelled:
		return true
	default:
		return false
	}
}
