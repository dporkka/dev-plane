# Model routing telemetry ownership

Dev Plane records software-task routing decisions and verifier outcomes. Bifrost owns volatile model/provider selection and authoritative provider-spend enforcement. Nulang Cloud is an execution and metering substrate and must not independently choose the LLM for a Dev Plane task.

## Stable boundary

Dev Plane emits stable routing provenance with each model call:

- `route`
- `route_source`
- `policy_version`
- task type and difficulty
- agent role and risk
- resolved model/provider
- token usage and latency
- `spend_authority`

When the call is routed through Bifrost, `spend_authority=gateway`. Dev Plane does not treat a local price estimate as authoritative and does not enforce local dollar limits against that incomplete ledger. Runtime, model-call, tool-call, shell-command, and concurrency limits remain local Dev Plane policy.

## Outcome signal

`routing_decisions` is the training/evaluation source for future adaptive routing. `model_usage.success` is only a provider-call success signal and must not be used as a software-task-success label.

A routing row is completed with:

- terminal run status
- structured final verifier output
- `verifier_passed`
- human-intervention requirement
- terminal error when present

The `run_tests` tool returns structured JSON for command failures, so verifier success is derived from its `passed` field rather than from a nil Go error.

## Nulang Cloud contract

Nulang Cloud should accept this provenance as opaque execution/metering metadata and preserve it on usage events. It should not reinterpret the semantic route or select a model. This keeps model-policy evolution isolated to Dev Plane/Bifrost while still allowing Nulang Cloud to correlate runtime resource usage with model-routing outcomes.

Recommended metadata keys for cross-system correlation:

```text
request_id
agent_run_id
task_id
step_number
route
route_source
routing_policy_version
model
provider
spend_authority
```

Nulang Cloud resource billing remains based on customer-understandable units such as compute, storage, network, durable steps, and inference tokens. Routing metadata is an attribution dimension, not a new billable unit.
