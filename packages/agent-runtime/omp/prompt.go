package omp

import (
	"context"
	"fmt"
)

// PromptStart captures the prompt acknowledgement fields that decide whether
// OMP will emit a later prompt_result for the request.
type PromptStart struct {
	ID           string
	AgentInvoked bool
}

// PromptStarter exposes OMP's prompt acknowledgement semantics without changing
// the minimal RPCClient contract used by alternate/test transports.
type PromptStarter interface {
	StartPrompt(ctx context.Context, message string) (PromptStart, error)
}

// StartPrompt sends a prompt and preserves data.agentInvoked. OMP uses
// agentInvoked=false when a builtin slash command completed synchronously; such
// prompts deliberately do not emit a later prompt_result frame.
func (c *StdioClient) StartPrompt(ctx context.Context, message string) (PromptStart, error) {
	var data struct {
		AgentInvoked bool `json:"agentInvoked"`
	}
	id, err := c.call(ctx, map[string]any{"type": "prompt", "message": message}, &data)
	if err != nil {
		return PromptStart{}, fmt.Errorf("omp prompt: %w", err)
	}
	return PromptStart{ID: id, AgentInvoked: data.AgentInvoked}, nil
}

var _ PromptStarter = (*StdioClient)(nil)
