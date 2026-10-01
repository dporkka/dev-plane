# Agent Runtime

`packages/agent-runtime` is Dev Plane's provider-neutral control-plane contract for coding agents.

It is intentionally separate from `packages/runtimes`:

- **agent-runtime** owns coding-agent conversations, capabilities, turns, items, resumption, and provider event normalization.
- **runtimes** owns workspace execution environments such as local worktrees, Docker containers, and remote runners.

## Durable model

```text
Workspace
└── Thread
    └── Turn
        └── Item
```

A `Thread` stores both the Dev Plane identifier and the provider-native thread identifier. The same distinction is preserved for turns and items so provider sessions can be resumed without coupling the control plane to provider ID semantics.

`Manager` is the durable entry point. It:

1. resolves a provider through `Registry`;
2. creates or resumes provider-native threads;
3. persists thread identity before returning it;
4. injects durable provider identity into new turns;
5. records turn/item events before forwarding them to callers;
6. cancels a run if persistence fails instead of continuing with an incomplete event history.

## Capability negotiation

Providers expose a `CapabilitySet`. Optional behaviors use small Go interfaces such as:

- `ResumeProvider`
- `InterruptProvider`
- `SteeringProvider`
- `CompactionProvider`
- `ApprovalProvider`
- `QuestionProvider`
- `RollbackProvider`

`Registry.Register` rejects providers that advertise interface-backed capabilities they do not implement.

This avoids provider-name conditionals throughout the rest of Dev Plane.

## Codex adapter

`codex.Provider` maps the OpenAI Codex app-server protocol onto the generic contract.

The current adapter supports:

- thread creation and resumption;
- turn start;
- turn steering;
- turn interruption;
- thread compaction;
- per-turn model selection;
- structured output schemas;
- normalized turn/item event streams.

`codex.StdioClient` starts:

```text
codex app-server --listen stdio://
```

and speaks newline-delimited JSON-RPC over stdin/stdout.

### Security defaults

Until Dev Plane routes Codex client-directed approval requests through the capability kernel, the adapter defaults to:

```text
approvalPolicy = on-request
sandbox        = read-only
```

Client-directed JSON-RPC requests that the adapter does not yet understand fail closed rather than being auto-approved.

Callers may explicitly configure a different sandbox or approval policy, but the adapter is not yet wired into the production API/worker execution path.

## Persistence

`packages/db` implements `agentruntime.Store` with:

- `agent_threads`
- `agent_turns`
- `agent_items`

The canonical migration is `024_create_agent_runtime_threads.sql`. Full provider payloads are retained as JSON while commonly queried ownership, provider, type, and status fields are indexed separately.

## Next integration boundary

The production API/worker should consume `Manager`, not call provider adapters directly.

Before enabling writable Codex execution in production, add provider approval/question routing through the existing capability kernel and approval service. Active-turn recovery after a control-plane crash should be treated separately from thread resumption.
