# Dev Plane shared Bifrost model router canary

The canonical LLM provider/model routing policy is staged in Adacavo
PR #5421 under bifrost/task-routes.json and Bifrost v2.6 routing rules.
Dev Plane should act as an authenticated client of the shared gateway,
not maintain a duplicate model/provider fallback catalog.

## Enable only in a test environment

Set:
- BIFROST_URL=https://STAGING-GATEWAY/v1 (the client also accepts root URL)
- BIFROST_API_KEY=VIRTUAL-KEY-ISSUED-FOR-DEV-PLANE
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

- Requests with explicit models, cost caps, structured output or context
  size over 32768 tokens fail closed while the canary mode is active.
  These require a separate capability/billing verification before expansion.
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
