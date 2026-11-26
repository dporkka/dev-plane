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
publication, verified numeric binding using mocked Dev Plane DB records, explicit stale-head rejection and
independent merged-state lookup). It terminates the server and removes the
temporary directory even on a test failure. It does not download a binary,
contact production, use operator credentials, or run in ordinary CI.

The harness seeds a branch through Gitea before publication; it does not
certify Git push credentials or workspace isolation. Temporary 405 handling in
the test is not a production merge retry policy.

## Verified Gitea repository bindings

Migration 027 reserves the tuple (canonical instance root, immutable upstream
numeric repository ID) for exactly one local repository, project and
organization. New Gitea connection requests remain **pending**. Earlier
connections are not automatically trusted even if their status says connected.
No webhook, tenant-provided clone URL, or shared API token constitutes a grant.

After applying migrations, an operator with independently established tenant
authorization must verify the exact upstream numeric ID from Gitea and issue
the grant using the configured GITEA_URL, GITEA_TOKEN and explicit DATABASE_URL:

```sh
go run ./apps/api/cmd/forge-bind-gitea \
  --repository-id LOCAL_REPO_UUID --project-id PROJECT_UUID \
  --organization-id ORGANIZATION_UUID --upstream-repository-id 123 \
  --apply
```

This command has no tenant HTTP endpoint and requires an explicit apply flag.
It checks the active project/organization, exact canonical instance and HTTP
clone URL, upstream numeric ID, name and repository metadata. Only a committed
grant marks the repository connected. Repeating the same grant refreshes
metadata; a conflicting grant cannot overwrite it or claim a reserved ID.
Soft deletion retains the upstream reservation. Reassignment or revocation
requires a separately designed administrative workflow; do not edit the table
to bypass authorization.

Initial approved-task provisioning and queued approval replay require a fresh
identity check, as does PR Factory publication, before PR creation. Gitea host-worktree
push is blocked by the isolated execution pilot below. Task creation enforces repository/project
and creator/organization consistency. Legacy GitHub-only sync and analysis
routes reject Gitea rather than marking an unverified connection connected or
reading an owner/name-only local cache.

This is a necessary, not sufficient, production authority boundary.
Gitea's name-based endpoints leave a check-to-use window if a repository
is renamed/recreated between verification and mutation. Restrict operator
credentials and remote permissions; do not enable webhook-driven task
creation or merge based on a receipt. The receipt consumer remains audit-only.

The binding and task-scope tests default to SQLite. To qualify PostgreSQL,
run them against two separate empty databases on a disposable loopback
PostgreSQL server with user `fixture` (replace the fixture port):

```sh
DEVPLANE_TEST_BINDING_POSTGRES_URL='postgres://fixture@127.0.0.1:55439/devplane_gitea_binding_test?sslmode=disable' \
DEVPLANE_TEST_TASK_SCOPE_POSTGRES_URL='postgres://fixture@127.0.0.1:55439/devplane_task_scope_test?sslmode=disable' \
  go test -run 'TestGiteaBindingOwnsNumericIdentity|TestInsertTaskEnforcesRepositoryProjectAndCreatorTenant' \
    ./apps/api/cmd/forge-bind-gitea ./apps/api/internal/handlers
```

Both tests refuse unexpected database names, hosts or nonempty schemas.

## Gitea isolated execution pilot

Run dispatch now resolves the durable run/task/project/repository/organization
chain regardless of whether a run is initial, retried, or handed off. A Gitea
run must use the legacy model-driven runner inside the task's own Docker
workspace (no local host path and no external-agent backend). Before dispatch,
verify the current upstream binding, task and run workspace IDs, actual
container labels, network isolation, read-only root, named volume, and kernel
capability/no-new-privileges status. Re-read the local chain after verification.
An arbitrary runtime-provider string, stale container, or old workspace cannot
substitute for a scoped workspace.

Provisioning requires a dedicated Gitea worker, a trusted already-installed
workspace image, and an independent read-only Git credential. The intentionally
single-repository pilot uses these operator settings:

- `WORKSPACE_RUNTIME=docker` and `DOCKER_WORKSPACE_IMAGE` (trusted, preferably
  digest-pinned local image; scoped provisioning never pulls).
- `GITEA_URL` and `GITEA_TOKEN` for binding/API verification, as before.
- `GITEA_CLONE_REPOSITORY_ID` (approved upstream numeric ID),
  `GITEA_CLONE_URL` (exact bound clone URL), `GITEA_CLONE_USERNAME`,
  `GITEA_CLONE_TOKEN` (dedicated read credential).
- Independently restrict the clone account at the forge to read-only access
  to that repository. The code checks local identity and distinct token
  values, not the account's upstream permission configuration.

Clone and checkout occur on the trusted host before a container starts. The
clone authenticates using a URL-scoped transient Git config environment; it
disables redirects, helpers, inherited Git config, proxy settings and submodule
recursion. Non-loopback HTTP is not permitted. The checkout and scoped
provisioning/verification Docker child processes receive no clone or operator
token; no arbitrary workspace env is
accepted, and no secret is stored in the repository or copied into the
container. Recheck the remote binding after provisioning before persisting a
ready workspace. Do not reuse old unlabelled workspaces.

Any process holding Gitea operator credentials must not execute or provision
non-Gitea workloads. Separate the Gitea execution worker/host from legacy
agents and route work appropriately before activating the pilot; the existing
shared NATS consumer does not provide dedicated routing yet. Filtering a
subprocess environment alone cannot protect a host credential from a local
agent with the same OS identity. All Gitea host-worktree pushes (and legacy
host-worktree pushes by credential-bearing publication processes) are blocked:
exporting a reviewed immutable revision to a trusted push broker remains a
separate task. Publishing an already-present remote branch is not proof that
the Docker workspace's changes were exported.

The following focused test uses real authenticated Git smart HTTP with
synthetic credentials; it checks redirect refusal, configuration isolation,
and the absence of credentials in the cloned repository. Optionally run a
real local container with a trusted, already-installed image containing
/bin/sh and /bin/cat:

```sh
DEVPLANE_TEST_GITEA_DOCKER_IMAGE=YOUR_LOCAL_IMAGE \
  go test -run '^TestScopedCloneUsesOnlyDedicatedCredential$' -v ./packages/runtimes
```

The fixture starts only a disposable local repository, container and volume
and cleans its own resources. The runtime can accept equivalent Podman
metadata only when the actual kernel capability sets are empty, the exact
named volume is mounted, and no_new_privs is enabled. Do not enable a
production worker until the actual deployment image/runtime and end-to-end
worker routing, revocation, and trusted artifact publication are qualified.

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
