# Platform Convergence

Dev Plane is the canonical software-development execution plane for David's product portfolio. Product repositories should consume it through APIs/SDKs rather than grow independent copies of repository intelligence, coding-agent runners, sandbox policy, approval workflows, security review, and PR delivery.

## Responsibility split

| Layer | Canonical owner | Responsibility |
| --- | --- | --- |
| Software-development control plane | Dev Plane | Task/spec lifecycle, agent runs, repository intelligence, workspaces, budgets, capability policy, approvals, tests, review, security scans, PR/release gates |
| Durable context / knowledge | AgentVault | Source-grounded research, decisions, lifecycle records, postmortems, hybrid retrieval, citations |
| Agent/runtime infrastructure | Nulang Cloud | Future runtime provider for durable/isolated execution, metering, snapshots, scheduling |
| Model routing | Bifrost / shared gateway | Provider routing, cost controls, caching, usage telemetry |
| Product/domain agents | Product repository | Domain-specific user-facing agents and workflows only |

## Integration rule

Before adding reusable development-agent infrastructure to Adacavo, API Factory, Websyt, Apex, or another product repo, first determine whether the capability belongs in Dev Plane. A product-specific adapter is preferred over a second implementation.

Examples that belong in Dev Plane:

- repository checkout/workspace lifecycle;
- Docker/remote/Nulang Cloud runtime providers;
- shell/file tool policy and capability approvals;
- generic coding/review/test/security agent roles;
- model and execution budgets;
- repository indexing/intelligence;
- GitHub branch/commit/PR delivery;
- generic audit/event schemas for software-development runs.

Examples that stay in products:

- Adacavo campaign/inventory/finance/sales agents;
- API Factory opportunity scoring, marketplace packaging, pricing, and revenue feedback;
- Apex document/mail/calendar assistants;
- Websyt audit-domain heuristics and customer-facing reports.

## AgentVault

Dev Plane's AgentVault integration is best-effort and must never block task execution. Lifecycle events should use stable `external_id` values so at-least-once delivery is idempotent.

Recommended event taxonomy:

```text
dev-plane.task.created
dev-plane.spec.approved
dev-plane.run.started
dev-plane.run.failed
dev-plane.run.completed
dev-plane.handoff.created
dev-plane.approval.requested
dev-plane.approval.resolved
dev-plane.review.completed
dev-plane.pr.created
dev-plane.release.completed
```

Persist high-value context and decisions, not every token or command log. AgentVault is the explanatory memory plane; Dev Plane remains the operational source of truth for active runs.

## API Factory

API Factory should hand validated product specs to Dev Plane and retain ownership of the economic loop:

```text
public demand signals
  -> opportunity extraction
  -> validation
  -> API product spec
  -> Dev Plane task
     -> isolated implementation
     -> tests/security/review
     -> approval
     -> PR/release artifact
  -> API Factory distribution
  -> usage/revenue/churn feedback
  -> next validation cycle
```

The initial integration is deliberately asynchronous. Do not remove API Factory's local generator until completion callbacks/webhooks allow Dev Plane to return an artifact and resume distribution reliably.

## Nulang Cloud runtime provider

Dev Plane's `packages/runtimes.Provider` is the intended integration point. A future provider should preserve the existing interface semantics rather than make the runner depend directly on Nulang Cloud internals.

Target capabilities:

- create isolated workspace/session;
- file read/write/patch and command execution;
- status/log streaming;
- snapshot/restore;
- cleanup;
- resource limits and cost metering;
- explicit network/secret capabilities.

This makes Dev Plane a serious dogfooding workload for Nulang Cloud while keeping both products independently usable.

## Migration strategy

1. Add adapters and prove parity before deleting duplicated code.
2. Keep product-facing APIs stable while swapping their implementation behind the boundary.
3. Move generic infrastructure first; leave domain logic in product repos.
4. Instrument cost, latency, reliability, and approval rates before and after migration.
5. Delete the duplicate implementation once production traffic has used the canonical path successfully.

The goal is fewer security-sensitive implementations and clearer ownership, not a single monorepo or forced runtime dependency.
