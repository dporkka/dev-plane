# Shared Platform Adoption

Dev Plane remains the semantic owner of software-development task execution. It should consume shared infrastructure contracts only where the semantics are truly product-neutral.

The canonical v1 contracts currently live under `dporkka/nulang-cloud/platform/spec/v1`. Dev Plane should use native Go adapters or generated types and must not depend on Nulang Cloud runtime crates.

## Shared candidates

| Dev Plane concern | Shared boundary |
|---|---|
| request/user/service correlation | `platform.context.v1` |
| Bifrost/direct model requests | platform AI inference request/result |
| generic model-visible tool calls | platform tool definition/call/result envelope |
| task/run lifecycle telemetry | platform execution event envelope |
| audit writes | platform audit event envelope |
| token/compute/service usage | platform usage event envelope |

## Dev Plane-owned semantics

Keep these local:

- task, run, repository, branch, pull-request, and deployment models;
- agent roles such as planner, implementer, reviewer, security reviewer, and release manager;
- workspace and sandbox lifecycle;
- shell/file/network capability policy;
- human approval and risk policy;
- GitHub/Linear/Slack/Discord integration semantics;
- release gates and deployment policy;
- Dev Plane persistence schema;
- product-specific authorization decisions.

The shared platform context carries opaque actor/tenant identifiers. Dev Plane remains responsible for deciding what an actor is allowed to do.

## Relationship to Adacavo

Adacavo may project an authorized product request into a Dev Plane development task. That adapter should map:

```text
Adacavo PlatformContext
  -> Dev Plane task creator/audit metadata
Dev Plane task/run state
  -> Platform execution events
  -> Adacavo projection
```

Do not make Dev Plane understand Adacavo tenant entities or OOH business objects.

## Migration order

1. Add a Go `PlatformContext` adapter at HTTP/event boundaries.
2. Normalize model gateway requests/results to the shared AI envelope.
3. Emit shared execution and audit events from existing task/run transitions.
4. Emit shared usage events for model and execution metering.
5. Keep existing Dev Plane models as the source of truth; delete only duplicate wire-envelope types.

## Versioning

Pin an exact merged platform contract revision. Do not load schemas over the network at runtime.

Breaking semantic changes require a new contract version. Optional additive fields may remain within v1 only when old consumers continue to validate.
