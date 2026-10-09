# Browser editing architecture: CodeMirror 6

Status: selected, 2026-10-09.

## Decision

**CodeMirror 6 is the sole primary browser code editor in Dev Plane.** Do not add
Monaco, a VS Code browser shell, or a Lapce-to-WASM port to the primary product
without a separate benchmark-supported architecture decision.

The existing `apps/web/components/code/CodeMirror.tsx` is the starting point.
It is intentionally lightweight today, with JavaScript/TypeScript, JSON, and
Markdown modes. Build professional IDE capabilities around it rather than
replacing its editor engine.

## Ownership and boundaries

- **Browser CodeMirror document:** the user's editable local draft, selection,
  undo history, accessibility, and cursor state.
- **Workspace runtime:** canonical on-disk file contents, terminal/dev server,
  language servers, and agent worktrees.
- **Dev Plane control plane:** tasks, agent attempts, approval decisions,
  workspace policy, and verification evidence.
- **Git:** authoritative revision of code delivered to review/integration.
- **Evidence store:** exact-head build, test, and browser verification receipts.

A human draft must not become an agent's shared writable checkout implicitly.
Each agent receives a distinct isolated worktree. The editor always indicates
which revision/worktree is being edited.

## Save contract

The browser maintains a saved baseline and a draft for every open file.

1. Editing to a different value marks the buffer dirty; editing back to the
   saved baseline clears dirty without an unnecessary write.
2. If a save is in flight, subsequently typed content remains dirty even after
   the earlier save is acknowledged.
3. Closing a dirty tab or unloading the browser requires a warning.
4. Reads now return a SHA-256 revision of exact file bytes. CodeMirror sends
   `expected_revision` on save; the API compares it with current bytes before
   writing and returns HTTP 409 on mismatch. The client does not discard drafts.
5. API-mediated writes on one host serialize through a filesystem lock. Local
   files are replaced with a same-directory rename to avoid partial reads.
   Files that did not exist may be created using an empty-string precondition.
6. Every HTTP and built-in agent write must supply `expected_revision`;
   absent revisions are rejected with HTTP 428 at the API boundary. This is
   intentionally a compatibility-breaking change for older API clients.

**Safety boundary:** This is a conditional API write, not yet a globally
atomic workspace transaction. Direct agent shell/git operations do not hold
the API lock; remote runner nodes may have independent lock directories; and
other programs can write files during the check/write interval. The API
and built-in agent tool now refuse missing preconditions; **direct provider,
shell, patch, and Git mutations are still outside this lock**. Treat these as
outstanding promotion blockers for shared writable agent/human workspaces,
rather than claiming lossless multi-writer editing.

**Implemented on the next stacked branch:** the per-file lock and revision
check have one shared implementation in `packages/runtimes`. HTTP editor
writes and built-in `write_file` tools (both local and Docker-backed) use
the same lock namespace. Agents must supply the SHA-256 token from
`read_file`, or an explicit empty revision to create a missing file. The
shared local writer rejects writes through symlink path components.

**Still blocked:** `run_command`, `run_tests`, `apply_patch`, Git
operations, direct runtime-provider calls and external tools can write files
without acquiring the shared lock. Cross-host runner clients also need
single-authority or transactional compare-and-swap enforcement at the
runtime owner; the current host-local lock is insufficient. Shell tooling
must therefore remain isolated in separate agent worktrees. This feature
is **not** a multi-writer-safe shared shell workspace.

**Next hardening:** route all workspace mutations through one runtime owner
with cross-process/node serialization, explicit file versions, atomic
local/restricted runtime writes, and capability checks. The path-symlink
checks do not replace fd-relative filesystem operations (for example,
Linux `openat2`) when untrusted local processes can modify the directories.
Continue using separate agent worktrees until the execution boundary is
proven.

Client-side route transitions need the same unsaved-buffer guard as browser
unload before the system can claim complete navigation protection.

## Near-term editing milestones

1. Complete **runtime-wide** conditional workspace-file writes (including
   direct agent writers and remote nodes) and conflict diff/rebase UX; cover
   agent-human concurrency and crash recovery in executable tests.
2. Add CodeMirror 6 language-server support via a scoped LSP bridge to the
   workspace. Include completion, hover, references, rename, code actions,
   diagnostics, and formatting. Gate LSP traffic by workspace identity.
3. Preserve per-file editor state across tabs: selection, undo/redo, scroll,
   folded ranges, and cursor locations; prevent stale async reads from
   overwriting opened buffers.
4. Provide in-editor AI patch previews with accept/reject per hunk, using
   explicit text revisions and source locations.
5. Connect the workspace preview to source maps and element selection so
   visual edits produce reviewable code patches.
6. Keep full test/build/Playwright verification and independent review
   separate from text editing. A successful editor save is not a passing build.

## Validation

Run the frontend's own checks from `apps/web`:

```sh
npm ci
npm run test
npm run typecheck
npm run lint
npm run build
```

Also test real simultaneous editing and agent writes on the deployed
workspace runtime, including delayed saves, remote conflicts, tab closing,
navigation, failed writes, and reconnects. Test commands are not a substitute
for those live multi-process checks.
