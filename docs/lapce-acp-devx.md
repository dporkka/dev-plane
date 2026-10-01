# Lapce + ACP development environment

`devx` is a thin, editor-independent ACP v1 client intended to run inside the Lapce integrated terminal while Dev Plane remains the orchestration/control-plane authority.

The design deliberately does **not** fork Lapce and does **not** replace repository-native worktree, verification, or landing workflows.

## Architecture

```text
Lapce
  |
  +-- editor / LSP
  |
  +-- integrated terminal
         |
         +-- devx chat / devx ask
                |
                +-- ACP v1 over newline-delimited JSON-RPC
                |
                +-- Codex ACP adapter
                +-- Gemini CLI --acp
                +-- custom ACP agent
                |
                +-- repository-native workflow
                       |
                       +-- Adacavo: scripts/devctl.mjs
                       +-- Nulang: Cargo gates
                       +-- Dev Plane: Make targets / runner
```

`devx` owns only the interactive ACP session. It does not own CI admission, pull-request creation, merging, or deployment.

## Build

From the Dev Plane repository root:

```bash
cd apps/cli
go test ./...
go vet ./...
go build -o ../../bin/devx ./cmd/devx
```

The default Codex adapter is launched on demand with:

```bash
npx -y @agentclientprotocol/codex-acp
```

Gemini uses its native ACP mode:

```bash
gemini --acp
```

## Basic use

Start a persistent interactive ACP session in the current project:

```bash
bin/devx chat --agent codex --cwd .
```

Run a one-shot review with write permissions denied:

```bash
bin/devx ask --agent gemini --permission deny \
  "Review the current diff for correctness and regressions"
```

The default permission mode is `ask`. Pressing Enter without selecting an option fails closed to the first reject option offered by the agent.

`--permission allow` chooses `allow_once` when available and only falls back to `allow_always` if the agent does not offer a one-shot allow option. Use it only inside disposable/isolated workspaces.

## Dev Plane task/run attachment

Attach a local ACP session to Dev Plane identifiers explicitly:

```bash
bin/devx chat \
  --agent codex \
  --task-id task_123 \
  --run-id run_456
```

or through the environment:

```bash
export DEV_PLANE_TASK_ID=task_123
export DEV_PLANE_RUN_ID=run_456
bin/devx chat --agent codex
```

The identifiers are:

1. stored in local session metadata under `$XDG_STATE_HOME/devx/sessions` (or `~/.local/state/devx/sessions`), and
2. attached to ACP `session/new`, `session/load`, and `session/prompt` requests under `_meta.devPlane`.

Agents may ignore `_meta`; Dev Plane tooling can still use the local state record to correlate the human session with a task/run.

## Session persistence

A chat keeps one ACP process and one ACP session alive across multiple turns.

Each session writes metadata containing the ACP session ID, working directory, selected agent, and optional Dev Plane task/run IDs.

If the agent advertises ACP v1 `loadSession`, reconnect with:

```bash
bin/devx chat \
  --agent codex \
  --cwd /absolute/path/to/repo \
  --resume-session sess_abc123
```

`devx` fails closed rather than calling `session/load` when the agent does not advertise the capability.

## Permission model

`devx` advertises an empty ACP client capability set. In particular, it does not advertise delegated filesystem writes or terminal execution.

Agent-side tools can still request authorization through the baseline ACP `session/request_permission` method. Supported policies:

- `ask` (default): render the agent's offered choices and require an explicit selection; blank/invalid input rejects.
- `deny`: choose `reject_once`, then `reject_always`, otherwise cancel.
- `allow`: choose `allow_once`, then `allow_always`, otherwise cancel.

The selected `optionId` is returned unchanged to the agent as required by ACP.

## Adacavo workflow

Do not let `devx` create or land Adacavo worktrees. Keep `scripts/devctl.mjs` authoritative.

Create the isolated runnable worktree first:

```bash
node scripts/devctl.mjs doctor
node scripts/devctl.mjs start fix-proposal-flow --link-env --install
```

Then open that worktree in Lapce and run `devx` from its terminal:

```bash
/path/to/dev-plane/bin/devx chat \
  --agent codex \
  --cwd . \
  --task-id ADACAVO-123
```

For autonomous implementation and exact-head review/CI admission, continue using:

```bash
node scripts/devctl.mjs pipeline fix-proposal-flow --prompt-file /tmp/task.md
node scripts/devctl.mjs checks fix-proposal-flow
node scripts/devctl.mjs land fix-proposal-flow
```

This keeps one authority for worktree lifecycle and merge evidence.

## Nulang workflow

Run `devx` inside an isolated Git worktree and keep Cargo as the judge:

```bash
git worktree add ../nulang-agent -b agent/runtime-fix
cd ../nulang-agent
/path/to/dev-plane/bin/devx chat --agent codex --cwd .
```

Fast verification during implementation:

```bash
cargo fmt --check
cargo check
cargo test -p nulang-ai
```

Full verification before handoff:

```bash
cargo fmt --check
cargo test
cargo test --features wasm-backend
```

## Dev Plane workflow

Use the task/run IDs that Dev Plane already owns:

```bash
DEV_PLANE_TASK_ID=<task-id> \
DEV_PLANE_RUN_ID=<run-id> \
bin/devx chat --agent codex --cwd .
```

The next integration step is to have the Dev Plane runner launch `devx`/ACP sessions and capture ACP lifecycle events in the existing run/event model. The interactive client should remain thin; execution policy, sandboxes, approvals, CI gates, and release decisions belong to Dev Plane.

## Custom ACP agents

Use an explicit executable and repeatable args without invoking a shell:

```bash
bin/devx chat \
  --agent-exec /absolute/path/to/my-acp-agent \
  --agent-arg --stdio \
  --cwd .
```

This avoids shell-string parsing and keeps process invocation deterministic.

## Lapce integration

The first integration is intentionally terminal-based:

1. Open the repository/worktree in Lapce.
2. Use Lapce for editing, LSP, navigation, and diff inspection.
3. Run `devx chat` in the integrated terminal.
4. Keep repository-native verification commands in the same worktree.

Do not maintain a private Lapce fork yet. Once `devx` is proven on real Adacavo/Nulang work, a thin Lapce UI can render the same ACP session/events without moving protocol or orchestration logic into the editor.

## Current scope

Implemented:

- stable ACP protocol version 1 negotiation,
- newline-delimited JSON-RPC stdio transport,
- `session/new`, `session/load`, `session/prompt`, and `session/cancel`,
- streamed agent text/thought/tool-call updates,
- interactive/fail-closed permission responses,
- Codex and Gemini defaults,
- custom ACP executables,
- local session metadata,
- Dev Plane task/run metadata attachment.

Intentionally deferred:

- ACP authentication UI,
- delegated client filesystem/terminal capabilities,
- rich diff rendering,
- MCP server configuration from `devx`,
- Dev Plane event persistence for ACP updates,
- Lapce-native ACP panel,
- experimental ACP v2.
