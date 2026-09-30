# Activity Sinks

Dev Plane emits high-level lifecycle activity through the provider-neutral `activity.Sink` interface.

```go
type Sink interface {
    Publish(ctx context.Context, event Event) error
}
```

The contract lives in `packages/activity`.

## Semantics

Activity sinks are **optional**. They are intended for external context stores, audit enrichment, knowledge systems, observability pipelines, or other consumers that want high-level Dev Plane lifecycle records.

Core request execution must not require an activity sink to be configured.

Current HTTP task creation treats activity delivery as best-effort: sink failures are logged and do not fail the originating request.

## Event model

`activity.Event` contains:

- `Type` — stable event taxonomy such as `dev-plane.task.created`;
- `Title` and `Text` — human-readable context;
- `Project` — optional logical project namespace;
- `Tags` — optional classification labels;
- `Metadata` — provider-neutral structured details;
- `ExternalID` — stable idempotency/correlation identifier;
- `CreatedAt` — event time.

Adapters may transform this representation into their native storage or transport model.

## Implementing a sink

```go
type MySink struct {
    // provider-specific client/config
}

func (s *MySink) Publish(ctx context.Context, event activity.Event) error {
    // Translate activity.Event to the external system.
    return nil
}
```

Configure the sink at the application composition boundary rather than importing it into core handlers.

## Built-in adapters

The API currently includes an optional AgentVault adapter. AgentVault is **not** part of the activity contract and is not required by Dev Plane. The adapter translates `activity.Event` into AgentVault's capture API.

Existing `AGENTVAULT_*` environment variables remain supported for backward compatibility.

## Adding another adapter

A new adapter should normally require no changes to `packages/activity` or handler domain logic.

If an external system needs additional provider-specific fields, keep them inside the adapter unless the information represents a genuinely general lifecycle concept shared by multiple sinks.
