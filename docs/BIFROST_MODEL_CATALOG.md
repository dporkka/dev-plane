# Bifrost-only model catalog for Dev Plane

Dev Plane delegates **API inference** to the Nulang Cloud Bifrost gateway in `DEV_PLANE_LLM_ROUTING_MODE=bifrost-only`. Native coding-agent subscriptions (such as an official Codex CLI) remain outside this API boundary.

## Required configuration

- `BIFROST_URL`: base gateway URL, for example `http://localhost:3000/v1` when the gateway runs locally. The client also accepts a base address without `/v1`.
- `BIFROST_API_KEY`: bearer credential accepted by the deployed gateway's actual Nulang Cloud identity/authentication chain. Do not assume that setting an arbitrary standalone API key creates a valid identity token.
- `BIFROST_MODEL_CATALOG_JSON`: operator-validated metadata for permitted provider-qualified model IDs.
- On the gateway, `BIFROST_ALLOWED_MODELS` must independently permit those same exact IDs.

**Illustrative metadata only** (intentionally invalid until the operator verifies pricing and changes `pricing_verified`):

```json
[
  {
    "id": "groq/openai/gpt-oss-120b",
    "max_context": 32768,
    "coding_strength": 8,
    "reasoning_strength": 8,
    "latency_ms": 500,
    "input_usd_per_1k": 0,
    "output_usd_per_1k": 0,
    "pricing_verified": false,
    "supports_structured_output": false,
    "supports_vision": false,
    "supports_function_calling": false
  }
]
```

The values above are **placeholders, not a claim about availability, accuracy, current pricing, or free quotas**. Confirm the model's real gateway support and provider terms, adjust all metadata, and explicitly mark `pricing_verified: true` only when justified. Zero price does not mean unmetered usage: provider free allowances can expire or be exhausted.

If the catalog is absent, invalid, contains duplicates, or includes unverified entries, the provider offers **no routable models**. Requesting a model not in the catalog fails without sending an upstream request. A response whose actual model differs from the requested upstream model is rejected, rather than silently reported as the requested model.

The gateway remains authoritative for tenant exclusions, model allowlists, actual token accounting, rate-limit windows, billing enforcement, and provider fallback policy. Dev Plane's local pricing metadata is an estimated admission check and **not** a substitute for an atomic gateway quota reservation.

## Rollout gate

Do not enable this mode in unattended production until all of the following hold: Bifrost PRs for tenant-safe fallback and requested models land; gateway bearer auth and model IDs pass a real HTTP integration test; streaming usage and quota reservations are durable; structured tool-call compatibility is validated end-to-end; and a bounded Git worktree coding task passes tests and independent review. Keep `DEV_PLANE_LLM_ROUTING_MODE` unset until then.
