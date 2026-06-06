package models

import "testing"

func TestMessageType_Constants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{MessageTypeHandoff, "handoff"},
		{MessageTypeReview, "review_comment"},
		{MessageTypeBlocker, "blocker"},
		{MessageTypeEscalation, "escalation"},
		{MessageTypeWatchdog, "watchdog"},
		{MessageTypeDecision, "decision"},
		{MessageTypeQuestion, "question"},
		{MessageTypeAnswer, "answer"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assertEqual(t, tt.got, tt.want)
		})
	}
}

func TestAgentMessage_IsBroadcast(t *testing.T) {
	t.Run("broadcast message", func(t *testing.T) {
		m := &AgentMessage{ToAgent: "broadcast"}
		assertEqual(t, m.IsBroadcast(), true)
	})

	t.Run("direct message", func(t *testing.T) {
		m := &AgentMessage{ToAgent: "planner"}
		assertEqual(t, m.IsBroadcast(), false)
	})
}

func TestAgentMessage_IsSystem(t *testing.T) {
	t.Run("system message", func(t *testing.T) {
		m := &AgentMessage{FromAgent: "system"}
		assertEqual(t, m.IsSystem(), true)
	})

	t.Run("agent message", func(t *testing.T) {
		m := &AgentMessage{FromAgent: "planner"}
		assertEqual(t, m.IsSystem(), false)
	})
}

func TestAgentMessage_IsHuman(t *testing.T) {
	t.Run("human message", func(t *testing.T) {
		m := &AgentMessage{FromAgent: "human"}
		assertEqual(t, m.IsHuman(), true)
	})

	t.Run("agent message", func(t *testing.T) {
		m := &AgentMessage{FromAgent: "implementer"}
		assertEqual(t, m.IsHuman(), false)
	})
}

func TestAgentMessage_Validate(t *testing.T) {
	t.Run("valid message", func(t *testing.T) {
		m := &AgentMessage{
			TaskID:      "task-1",
			FromAgent:   "system",
			ToAgent:     "broadcast",
			MessageType: MessageTypeHandoff,
			Content:     "Hello agents",
		}
		assertError(t, m.Validate(), false)
	})

	t.Run("missing task_id", func(t *testing.T) {
		m := &AgentMessage{
			FromAgent:   "system",
			ToAgent:     "broadcast",
			MessageType: MessageTypeHandoff,
			Content:     "Hello agents",
		}
		err := m.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "agent_message task_id is required" {
			t.Errorf("got %q, want %q", err.Error(), "agent_message task_id is required")
		}
	})

	t.Run("missing from_agent", func(t *testing.T) {
		m := &AgentMessage{
			TaskID:      "task-1",
			ToAgent:     "broadcast",
			MessageType: MessageTypeHandoff,
			Content:     "Hello agents",
		}
		err := m.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "agent_message from_agent is required" {
			t.Errorf("got %q, want %q", err.Error(), "agent_message from_agent is required")
		}
	})

	t.Run("missing to_agent", func(t *testing.T) {
		m := &AgentMessage{
			TaskID:      "task-1",
			FromAgent:   "system",
			MessageType: MessageTypeHandoff,
			Content:     "Hello agents",
		}
		err := m.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "agent_message to_agent is required" {
			t.Errorf("got %q, want %q", err.Error(), "agent_message to_agent is required")
		}
	})

	t.Run("missing message_type", func(t *testing.T) {
		m := &AgentMessage{
			TaskID:    "task-1",
			FromAgent: "system",
			ToAgent:   "broadcast",
			Content:   "Hello agents",
		}
		err := m.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "agent_message message_type is required" {
			t.Errorf("got %q, want %q", err.Error(), "agent_message message_type is required")
		}
	})

	t.Run("missing content", func(t *testing.T) {
		m := &AgentMessage{
			TaskID:      "task-1",
			FromAgent:   "system",
			ToAgent:     "broadcast",
			MessageType: MessageTypeHandoff,
		}
		err := m.Validate()
		assertError(t, err, true)
		if err != nil && err.Error() != "agent_message content is required" {
			t.Errorf("got %q, want %q", err.Error(), "agent_message content is required")
		}
	})
}
