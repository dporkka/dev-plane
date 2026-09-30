package agentruntime

import "context"

// EventSink receives provider-neutral runtime events after they are durably
// persisted and before they are exposed to the caller. A sink failure stops
// delivery so control-plane side effects cannot lag behind visible runtime state.
type EventSink interface {
	HandleEvent(ctx context.Context, thread Thread, event Event) error
}
