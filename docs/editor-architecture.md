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
4. Before a write, the current client performs a **best-effort** read comparison
   against the saved baseline. If it differs, the client blocks the write and
   offers copying the local draft or explicitly reloading the remote version.

**Important:** Step 4 is not an atomic concurrency guarantee. Another agent
could write between the client read and write. The next backend change must
implement conditional writes in the workspace runtime using an expected
content revision/digest, with write-side synchronization and a 409 conflict
response. Every editor, agent tool, and runtime writer must participate in
that enforcement; browser-only preflight cannot be advertised as lossless
multi-writer editing.

Client-side route transitions need the same unsaved-buffer guard as browser
unload before the system can claim complete navigation protection.

## Near-term editing milestones

1. Complete conditional workspace-file writes (server-side compare-and-swap)
   and conflict diff/rebase UX; cover concurrent agent-human writes in tests.
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
