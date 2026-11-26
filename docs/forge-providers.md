# Forge providers

Dev Plane treats the Git forge as an external collaboration and source-control authority, not as the agent execution plane.

The intended architecture is:

```text
GitHub / Gitea
      |
      | repositories, issues, pull requests, webhooks, checks
      v
Dev Plane
      |
      | goals, work items, agents, policy, verification, evidence
      v
Workspace provider
      |
      v
local / OCI / Nulang Cloud
```

## Authority boundary

The forge owns:

- Git repository hosting;
- repository membership and ordinary forge permissions;
- issues and pull requests;
- releases and packages;
- forge webhooks and status/check presentation.

Dev Plane owns:

- Goals and WorkItems;
- AgentRuns and model routing;
- repository context and Project Brain;
- policy and approvals;
- change orchestration;
- verification and immutable evidence;
- merge/deployment intent and audit provenance.

Workspace providers own isolated command/file execution. Dev Plane must not depend on a particular sandbox implementation in order to support a forge.

## `gateway.Forge`

The first provider-neutral contract intentionally contains only semantics required by current Dev Plane workflows:

- repository discovery;
- pull-request creation;
- pull-request merge with optional exact-head binding;
- repository webhook creation/deletion.

Provider-specific capabilities should not be added to this interface merely because an API exposes them. Add a cross-forge semantic only when a Dev Plane workflow actually requires it.

## GitHub

`GitHubForge` adapts the existing `GitHubGateway`. Existing GitHub-specific callers remain supported during migration.

## Gitea

`GiteaForge` uses Gitea's `/api/v1` API and supports self-hosted instance URLs, including installations hosted under a URL subpath.

The v1 provider currently supports:

- token-authenticated repository list/get;
- pull-request creation;
- merge, squash, and rebase;
- exact reviewed-head binding through `head_commit_id`;
- webhook create/delete.

### Transport boundary

Supply an operator-controlled instance URL and narrowly scoped token. Prefer HTTPS;
HTTP is appropriate only for trusted local or otherwise protected development
networks. The adapter preserves a supplied HTTP transport (for example, a private
CA) but caps requests at ten seconds and refuses all redirects. It rejects
non-2xx status codes without including provider-controlled error bodies and
limits decoded response input to 4 MiB. Credentials must never be embedded in
instance URLs or passed to agents. Infrastructure must enforce instance
allowlisting/egress policy where repository connection settings are user supplied.

### Draft pull requests

Gitea 1.25's create-pull-request request schema does not expose a draft input even though pull-request responses expose draft state. Dev Plane therefore returns `ErrUnsupportedForgeCapability` when a caller requests `Draft=true` through this provider.

This is deliberate. The provider must not silently weaken a requested draft into a normal pull request or encode semantics through title conventions.

## Credentials

`ForgeCredential` currently contains an API access token. Treat this as a forge API credential only.

Git transport credentials are a separate concern. In particular, GitHub's `x-access-token` HTTPS convention must not become the generic Gitea credential model. The PR Factory migration should introduce a separate Git push credential/transport boundary before enabling end-to-end Gitea branch publication.

## Runtime PR publication

The API request path and worker approval path both construct the same PR
Factory. Set the same environment on both services; restart the worker when
changing its configuration.

- `FORGE_PROVIDER=github` (default): existing `GITHUB_TOKEN` behavior.
- `FORGE_PROVIDER=gitea`: set `GITEA_URL` to the operator-controlled instance
  root, and `GITEA_TOKEN` to a scoped API token. An unsupported or incomplete
  selection fails closed, without using GitHub credentials.
- For HTTPS push, explicitly configure **both** `GITEA_GIT_USERNAME` and
  `GITEA_GIT_PASSWORD`; neither is inferred from the API token. Keep both
  unset for SSH. The effective fetch and push destination must match the
  stored repository forge authority.

The current runtime selects one instance per service process. A repository
with another provider or instance is rejected before publication. Keep
credential-bearing Git execution in a trusted isolated workspace; checking
remote destinations alone does not make arbitrary agent-controlled Git
configuration safe. The API's existing GitHub-only merge and deployment
endpoints reject repositories with Gitea authority until provider-neutral
merge and deployment semantics are wired. Signed Gitea ingress records
externally bound receipts but does not yet dispatch agent tasks in the worker.

### Signed Gitea webhook receipts

Apply migration 026 before enabling the worker. Configure the same operator-
controlled instance root (`GITEA_URL`) and instance-specific signing secret
(`GITEA_WEBHOOK_SECRET`) in the API and worker; register that secret and
`POST /api/v1/webhooks/gitea` in Gitea. Use a different secret for every
instance; never reuse the GitHub signing key. The endpoint is disabled unless
both settings are valid. This is independent of `FORGE_PROVIDER`.

Ingress verifies the body HMAC and requires the signed repository numeric ID,
full name, and canonical HTML URL to match the configured root (including
any installation subpath). No inbound Host or forwarding header selects the
instance. The envelope preserves the original signed bytes through base64
encoding, so the worker independently verifies the signature and instance
before writing a receipt. Broker publishers still require trusted access:
configure NATS TLS and subject authorization, and protect its storage because
in-flight messages contain the original body and signature. A broker with a
smaller message limit can reject a delivery (HTTP 503); size for base64
expansion or constrain accepted webhook sizes operationally.

The receipt ledger stores only instance URL, signed external repository ID
and name, body SHA-256, delivery ID, event type, and receipt time. Atomic
SQL uniqueness constraints suppress repeated delivery IDs and identical
signed bodies even if a replay changes an unsigned event/delivery header.
This is receipt deduplication, not event sequencing or task exactly-once
execution. Event type and delivery headers are **untrusted hints**; do not
use either as authorization. Equal signed bytes under different delivery
IDs intentionally produce one receipt.

The existing `webhooks_star` durable subscription now uses manual ACK.
Persistence/configuration failures leave received Gitea events unacknowledged
for retry; invalid signatures and malformed/legacy envelopes are discarded.
Processed/failed notification subjects are drained without dispatching them
again. GitHub/Linear retain their prior error-ACK behavior; GitHub issue
routing also excludes Gitea rows and rejects ambiguous GitHub matches.
Deploy the migration and compatible worker with the API, and drain or
reconcile old webhook deliveries before upgrade or signing-key rotation
(the old envelopes did not preserve authenticated bytes). Retain receipts
for the lifetime of their signing key; there is no authenticated timestamp
that makes blindly expiring the replay ledger safe.

**No tenant binding or agent execution yet:** a local repository connection
stores user-provided name/URL, not verified ownership of the upstream
repository ID. Do not route receipts to projects/users or treat merge/review
events as policy approval. The next phase must establish explicit
instance + immutable upstream repository ID authorization and independent
provider state verification before any task/PR mutation.

For optional local broker qualification, with a trusted `nats-server` binary:

```sh
DEVPLANE_TEST_NATS_SERVER=/absolute/path/to/nats-server \
  go test -race -run '^TestLiveGiteaReceiptJetStreamRedelivery$' ./apps/worker/internal/webhooks/...
```

The test starts a loopback-only ephemeral JetStream broker and migrated
SQLite fixture, reuses a synthetic existing wildcard durable, injects a
database failure, observes real redelivery, and verifies that replay and
outcome messages leave only one receipt. It never connects to `NATS_URL`.
The binary, broker state and fixture are independent of production.

A second opt-in test exercises the same receipt insert across independent
PostgreSQL connection pools. Use only a fresh empty `devplane_gitea_receipts_test`
database on an isolated loopback PostgreSQL process with user `fixture`:

```sh
DEVPLANE_TEST_GITEA_RECEIPTS_POSTGRES_URL='postgres://fixture@127.0.0.1:55439/devplane_gitea_receipts_test?sslmode=disable' \
  go test -race -run '^TestPostgresGiteaReceiptConcurrentWriters$' ./apps/worker/internal/webhooks/...
```

Substitute the actual ephemeral fixture port; the test refuses a different
database name, host, user, query options or nonempty public schema. Destroy
the disposable PostgreSQL instance after the test.

### Reproducible disposable Gitea qualification

With Go dependencies/toolchain already cached and a trusted, checksum-verified
Gitea 1.27.3 executable, run from the repository root:

```sh
python3 packages/prfactory/testdata/live_gitea.py --binary /path/to/gitea
```

The opt-in runner creates an isolated SQLite-backed Gitea server listening only
on loopback, a synthetic private repository and separate bootstrap/scoped API
tokens. It runs a build-tagged PR Factory integration test (real Gitea
publication, mocked Dev Plane DB records, explicit stale-head rejection and
independent merged-state lookup). It terminates the server and removes the
temporary directory even on a test failure. It does not download a binary,
contact production, use operator credentials, or run in ordinary CI.

The harness seeds a branch through Gitea before publication; it does not
certify Git push credentials or workspace isolation. Temporary 405 handling in
the test is not a production merge retry policy.

## Migration sequence

1. Qualify the `gateway.Forge` contract and both adapters on the repository-native Go toolchain.
2. Migrate PR Factory from its private GitHub creator interface to `gateway.Forge`.
3. Separate Git push credentials from forge API credentials.
4. Add repository-level forge provider/base-URL authority.
5. Route webhook ingestion through provider-aware verification.
6. Normalize commit/check status publication so verified evidence can be surfaced on both GitHub and Gitea.
7. Dogfood Dev Plane against an unmodified Gitea deployment before adding any custom forge UI.

## Non-goals

Do not fork Gitea to add Dev Plane features unless an unavoidable forge-level primitive is missing. Agent orchestration, workspace execution, Project Brain, verification, proof, and policy belong in Dev Plane or its workspace providers, not inside the forge.
