package models

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// MessageType constants for agent-to-agent communication.
const (
	MessageTypeHandoff    = "handoff"
	MessageTypeReview     = "review_comment"
	MessageTypeBlocker    = "blocker"
	MessageTypeEscalation = "escalation"
	MessageTypeWatchdog   = "watchdog"
	MessageTypeDecision   = "decision"
	MessageTypeQuestion   = "question"
	MessageTypeAnswer     = "answer"
)

// AgentMessage represents a durable message in the agent mailbox system.
// Messages enable asynchronous communication between agents, humans,
// and the system for coordination, handoffs, and escalation.
type AgentMessage struct {
	ID          string         `json:"id"`
	TaskID      string         `json:"task_id"`
	RunID       *string        `json:"run_id,omitempty"`
	FromAgent   string         `json:"from_agent"` // agent role or "human" or "system"
	ToAgent     string         `json:"to_agent"`   // agent role or "broadcast"
	MessageType string         `json:"message_type"`
	Content     string         `json:"content"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Validate checks that the agent message has required fields.
func (m *AgentMessage) Validate() error {
	if m.TaskID == "" {
		return errors.New("agent_message task_id is required")
	}
	if m.FromAgent == "" {
		return errors.New("agent_message from_agent is required")
	}
	if m.ToAgent == "" {
		return errors.New("agent_message to_agent is required")
	}
	if m.MessageType == "" {
		return errors.New("agent_message message_type is required")
	}
	if m.Content == "" {
		return errors.New("agent_message content is required")
	}
	return nil
}

// IsBroadcast returns true if the message is addressed to all agents.
func (m *AgentMessage) IsBroadcast() bool {
	return m.ToAgent == "broadcast"
}

// IsSystem returns true if the message originated from the system.
func (m *AgentMessage) IsSystem() bool {
	return m.FromAgent == "system"
}

// IsHuman returns true if the message originated from a human.
func (m *AgentMessage) IsHuman() bool {
	return m.FromAgent == "human"
}

// NullAgentMessage returns an AgentMessage from sql.Null fields.
func NullAgentMessage(
	id sql.NullString,
	taskID sql.NullString,
	runID sql.NullString,
	fromAgent sql.NullString,
	toAgent sql.NullString,
	messageType sql.NullString,
	content sql.NullString,
	metadata sql.NullString,
	createdAt sql.NullTime,
) *AgentMessage {
	m := &AgentMessage{}
	if id.Valid {
		m.ID = id.String
	}
	if taskID.Valid {
		m.TaskID = taskID.String
	}
	if runID.Valid {
		r := runID.String
		m.RunID = &r
	}
	if fromAgent.Valid {
		m.FromAgent = fromAgent.String
	}
	if toAgent.Valid {
		m.ToAgent = toAgent.String
	}
	if messageType.Valid {
		m.MessageType = messageType.String
	}
	if content.Valid {
		m.Content = content.String
	}
	if metadata.Valid {
		var meta map[string]any
		if err := json.Unmarshal([]byte(metadata.String), &meta); err == nil {
			m.Metadata = meta
		}
	}
	if createdAt.Valid {
		m.CreatedAt = createdAt.Time
	}
	return m
}
