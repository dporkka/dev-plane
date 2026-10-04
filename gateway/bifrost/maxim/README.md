# Maxim Bifrost adaptive model routing

This directory is the current Dev Plane model-gateway deployment path. It is intentionally separate from the older `gateway/bifrost/bifrost.yaml.template`, which describes a hypothetical gateway and is retained for compatibility with existing verification material.

## Architecture

Dev Plane owns task semantics, software-agent policy, operational limits, and task-outcome telemetry. It sends a stable semantic route plus deterministic context such as task type, difficulty, agent role, and risk. Bifrost owns volatile model/provider resolution, provider-spend enforcement, current pricing, provider health, fallback, and request-level routing. Nulang Cloud remains the execution substrate and should carry routing metadata for observability/metering rather than choosing LLMs.

The default path is:

```text
Dev Plane agent
  -> route/auto + task/difficulty/risk headers
  -> Maxim Bifrost
       1. deterministic hard filters / explicit semantic routes
       2. native semantic complexity classifier for ambiguous auto traffic
       3. CEL model selection
       4. OpenRouter provider routing/failover
  -> selected model
  -> Dev Plane verifiers/tests
  -> routing_decisions outcome telemetry
  -> future shadow/learned routing evaluation
```

## Initial model policy

| Workload | Primary | Fallback |
| --- | --- | --- |
| Cheap/simple/general | `z-ai/glm-5.3-flash` | DeepSeek V4.1 Flash |
| Hard coding/debug/test | `deepseek/deepseek-v4.1-flash` | GLM-5.3 Flash |
| Complex long-horizon coding | `deepseek/deepseek-v4.1-flash` | GLM-5.3 |
| Hard architecture/review | `z-ai/glm-5.3` | DeepSeek V4.1 Flash |
| Explicit security/high reasoning | `z-ai/glm-5.3` | DeepSeek V4.1 Flash |

These mappings are gateway configuration, not application code. Change them as benchmark and production outcome data changes.

## Run locally

1. Create the Bifrost application directory and copy the config:

```bash
cd gateway/bifrost/maxim
mkdir -p data
cp config.json.example data/config.json
```

2. Export credentials. OpenRouter serves the inference models; the OpenAI key is used only for the inexpensive semantic-complexity embedding request.

```bash
export OPENROUTER_API_KEY='...'
export OPENAI_API_KEY='...'
export BIFROST_ENCRYPTION_KEY='replace-with-a-long-random-secret'
```

3. Start the pinned gateway:

```bash
docker compose up -d
curl -fsS http://localhost:8083/health
```

4. Point Dev Plane at Bifrost:

```bash
export BIFROST_URL='http://localhost:8083/v1'
```

For an unauthenticated local gateway, Dev Plane's current `BifrostProvider` still requires a non-empty `BIFROST_API_KEY` to mark the provider available. Prefer creating a Bifrost virtual key even locally rather than using a dummy value.

## Production authentication and spend authority

Do not expose an unauthenticated Bifrost instance. Before enabling inference authentication:

1. Create a Bifrost virtual key for Dev Plane. Give it access only to the `openrouter` provider/models required by the routing policy and set the authoritative provider-spend budget/rate limit there.
2. Set `client.enforce_auth_on_inference` to `true` in `data/config.json`.
3. Set Dev Plane's `BIFROST_API_KEY` to that `sk-bf-*` virtual key. Dev Plane sends it as an OpenAI-style bearer credential, which Bifrost accepts for virtual-key authentication.
4. Terminate TLS at the service boundary and do not expose the management UI publicly.

When Bifrost is configured, Dev Plane marks routing decisions with `spend_authority=gateway`. It does not put stale local model-price estimates into `model_usage.cost` or enforce local dollar limits against an incomplete ledger. Dev Plane still enforces runtime, model-call, tool-call, shell-command, and concurrency limits. Direct-provider routing retains `spend_authority=dev-plane` and the existing local dollar-budget behavior.

The semantic classifier's direct OpenAI key is stored only in Bifrost and is not available to Dev Plane agents.

## Why native Bifrost complexity routing first

Do **not** put a large classifier in front of every request initially. Bifrost already exposes `complexity_tier` (`SIMPLE`, `MEDIUM`, `COMPLEX`) using semantic embeddings and supports combining it with deterministic CEL rules. The config gives deterministic task/difficulty signals higher precedence and uses semantic complexity only for ambiguous `route/auto` traffic.

This is a baseline, not the final router. Generic complexity is not the same as `P(task succeeds | task, model)`.

## Routing outcome telemetry

Migration `025_create_routing_decisions.sql` records one row per successful model call with the routing inputs and the eventual software-task outcome. The record includes:

- task/run/step identifiers
- semantic route, route source, and routing-policy version
- task type, difficulty/risk, and agent role
- selected model/provider and spend authority
- prompt/completion tokens, estimated cost, and latency
- provider-call success
- final run status
- structured verifier results and verifier pass/fail
- whether human intervention was required

The final-test parser reads the structured `passed` field emitted by `run_tests`; a nil Go error alone is not treated as verifier success. Failed and paused runs also feed terminal outcome/human-intervention state into routing telemetry.

Do not train from `model_usage.success`: it means the provider call completed successfully, not that the software task was correct.

## OpenJev experiment

OpenJev should be evaluated in **shadow mode**, not made the production model selector yet.

Recommended experiment:

1. Capture the same routing input sent to Bifrost.
2. Ask a small Jev decision model (start with `laya-1.0` or `verdict-1.4`, not the 26B model) which route/model tier it prefers.
3. Do not alter production routing.
4. Join the shadow decision to the eventual `routing_decisions` verifier outcome.
5. Compare utility against the Bifrost-only baseline.

Promote Jev only if it improves end-to-end utility after accounting for classifier latency and cost. Classifier confidence alone is not an acceptance criterion.

## Outcome-driven routing

Once enough verified outcomes exist, estimate model utility from Dev Plane's own workload rather than generic difficulty labels. A useful objective is approximately:

```text
utility(model, task)
  = P(success | task, model) * task_value
  - lambda_cost * expected_cost
  - lambda_latency * expected_latency
  - lambda_retry * expected_repair_cost
  - risk_penalty
```

Start with a simple contextual bandit / Thompson-sampling layer per semantic route. Keep 2-5% exploration only for low-risk traffic; use shadow replay for security, release, and other high-risk work.

## Production acceptance criteria

Do not judge the router by classifier accuracy alone. Compare policies on a held-out set of real Dev Plane tasks and require improvements in:

- verifier pass rate on the first attempt
- final task success rate
- cost per successful task
- p50/p95 latency per successful task
- repair/retry count
- human intervention rate
- security/regression escape rate

The correct router is the policy with the best task-level utility under your latency, cost, and risk constraints, not the classifier with the highest standalone benchmark score.
