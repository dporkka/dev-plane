# Bifrost AI Gateway — example configurations

These files are **template/example configurations** for a hypothetical
"Bifrost" AI gateway layer described in the Multi-Agent Developer Mesh spec.
They are not tied to any specific off-the-shelf product; adapt the API group,
field names, and image references to the gateway you actually deploy.

The examples demonstrate:

- `cluster.yaml` — gossip-synchronized gateway cluster binding only to the
  Tailscale overlay.
- `regulator.yaml` — network-wide token-bucket rate limiter with multiple
  queues.
- `semantic-cache.yaml` — local semantic cache with a cosine similarity
  threshold of **0.85**.

Treat these as a starting point. Remove any fields that your gateway does not
support, and keep the operational surface as small as possible.
