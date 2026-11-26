# Dev Plane shared Bifrost model router canary

The canonical LLM provider/model routing policy is staged in Adacavo
PR #5421 under bifrost/task-routes.json and Bifrost v2.6 routing rules.
Dev Plane should act as an authenticated client of the shared gateway,
not maintain a duplicate model/provider fallback catalog.

## Enable only in a test environment

Set:
- BIFROST_URL=https://STAGING-GATEWAY/v1 (the client also accepts root URL)
- BIFROST_API_KEY=VIRTUAL-KEY-ISSUED-FOR-DEV-PLANE (server-side only)
- BIFROST_SHARED_ROUTING_ENABLED=true

The switch is false by default. When true, Dev Plane routes eligible model
calls through Bifrost and deliberately fails closed if the shared provider
is missing or returns an error. It does not silently bypass the gateway
by sending the request directly to other AI providers.

Supported aliases:
- code / refactor / test -> bifrost/coding
- review -> bifrost/code-review
- architecture / debugging -> bifrost/agent
- documentation -> bifrost/summarize
- simple -> bifrost/cheap
- hard / expert difficulty -> bifrost/agent

These aliases are upstream routing-rule names, not standalone model IDs.
They require the staged MaximHQ Bifrost routing rules to be enabled in
staging before this switch can work.

## Safety constraints

- Requests with explicit models, cost caps, or context size over 32768
  characters fail closed while the canary mode is active.
- Structured JSON actions are forwarded to Bifrost using OpenAI-compatible
  response_format=json_object. Provider JSON-mode support and schema
  conformance still require staging validation before enabling live agents.
- Billing is NOT READY: the old Go ModelInfo cost catalog contains stale
  assumptions; shared task aliases do not have guaranteed per-token prices.
  The current OpenAI-compatible client may report a zero estimated cost
  for an unmapped alias. Do not use this canary for customer billing.
- Provider responses now preserve the actual model identifier if returned
  in the upstream JSON. Use provider metering for final charge reconciliation.
- Do not enable a global product key or lift model allowlists to accommodate
  an unavailable route. Product/tenant-specific virtual keys stay separate.
- Run a provider-status and 429/5xx fallback test in staging first. Do not
  promote to mainline production until cost per successful task and
  audit correlation have been measured.
- The selected route is text-only until tool-calling and structured-output
  schemas have passed integration evaluation.

## Verification

    cd apps/api
    go test ./internal/modelrouter -count=1

All new HTTP verification uses a local test server and test-only keys.
No real provider invocation or production configuration update is performed.

In shared mode Dev Plane now authenticates with Bifrost's dedicated
X-Bf-Vk governance header, not Authorization Bearer. HTTPS is mandatory
for non-loopback addresses. The legacy mode retains its existing
Authorization Bearer behavior for backward compatibility.

This PR does not create a virtual key or install any secrets. Provision
a separate Dev Plane virtual key with a restricted model/provider allowlist
and monthly budget before setting the flag. The optional PR Triage
consumer must use a distinct virtual key for separate auditability.

## Legacy model-driven runner integration

The production default of apps/api/internal/agentrunner.NewRunner wires
modelrouter.NewRouter with modelrouter.AllProviders. With the environment flag
enabled, this is an actual execution path, not a dormant HTTP adapter.
Agent-runner requests include StructuredReq=true for JSON model actions;
the shared Bifrost adapter forwards response_format=json_object accordingly.
A localhost end-to-end test exercises NewRunner through the HTTP gateway
boundary. Invalid or non-JSON model actions remain rejected by the
existing agent-runner parseModelAction validation.

The separately selected agent_runtime external execution backend does not
use this legacy Go model router; that backend needs a separate migration
before claiming 100% of Dev Plane traffic is routed through shared Bifrost.
