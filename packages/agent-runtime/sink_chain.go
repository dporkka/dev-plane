package agentruntime

import (
	"context"
	"errors"
	"fmt"
)

// EventSinkChain forwards each durable runtime event through a fixed sequence of
// control-plane side effects. Sinks run in declaration order and processing
// stops at the first failure so later side effects never outrun earlier durable
// authorities such as the append-only runtime ledger.
type EventSinkChain struct {
	sinks []EventSink
}

// NewEventSinkChain creates an immutable-by-construction ordered sink chain.
func NewEventSinkChain(sinks ...EventSink) (*EventSinkChain, error) {
	if len(sinks) == 0 {
		return nil, errors.New("event sink chain requires at least one sink")
	}
	copied := make([]EventSink, len(sinks))
	for i, sink := range sinks {
		if sink == nil {
			return nil, fmt.Errorf("event sink %d is nil", i+1)
		}
		copied[i] = sink
	}
	return &EventSinkChain{sinks: copied}, nil
}

// HandleEvent runs sinks in declaration order. The first error is returned with
// its one-based position and prevents all later sinks from observing the event.
func (c *EventSinkChain) HandleEvent(ctx context.Context, thread Thread, event Event) error {
	if c == nil || len(c.sinks) == 0 {
		return errors.New("event sink chain is not configured")
	}
	for i, sink := range c.sinks {
		if err := sink.HandleEvent(ctx, thread, event); err != nil {
			return fmt.Errorf("event sink %d: %w", i+1, err)
		}
	}
	return nil
}

var _ EventSink = (*EventSinkChain)(nil)
