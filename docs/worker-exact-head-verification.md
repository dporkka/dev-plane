# Exact-HEAD worker verification rollout

This worker integration is intentionally staged behind `DEVPLANE_EXACT_HEAD_VERIFICATION`.

| Value | Behavior |
|---|---|
| `required` | Run completion fails closed unless a persisted WorkItem, immutable workspace base and passing exact-HEAD verification are available. Review must certify the same base and head. |
| unset or `legacy` | Existing run-completion behavior; no independent verifier guarantee. |
| any other value | Worker refuses to start. |

## Required preconditions

- Immutable workspace `settings.base_sha` from the pre-agent provisioning flow (#185).
- A task-keyed, trusted `work_items` row in the `023` schema, with nonempty executable `RequiredGates`, based on approved task specifications and repository policy (#186 still requires production onboarding).
- A trusted root `devplane.yaml` defining executable verification profiles (#139) and a suitable isolated runtime with a stable session.
- Verification hardening and real runtime smoke on #188; explicit review-to-approval checks on #189.

Enabling `required` without WorkItem provisioning is **deliberately fail closed**.
Do not switch production tenants until the complete onboarding flow is qualified.

## Verification flow

1. Ensure the final agent run has no outstanding mailbox handoff.
2. Load run, task, workspace, runtime session and immutable base from the database.
3. Load the task's canonical WorkItem. Reject mismatched bases, unsupported state and no executable gate.
4. Resolve the workspace candidate with `git rev-parse HEAD`; require it to descend from the base and have tracked changes.
5. Check for dirty tracked/untracked files; execute repository verification profiles through the runtime provider.
6. Validate the saved evidence bundle against candidate HEAD. Check the workspace remains clean and HEAD unchanged.
7. Run code review. Require `ReviewReport.DiffSummary.BaseSHA/HeadSHA` and run ID to match the verifier receipt before moving the task to `reviewing` or publishing `review.completed`.

## Limitations

- This controls the **worker final-run path**. Manual review endpoints, downstream approval and PR publication must independently check exact-HEAD evidence; this is not yet a complete authorization chain.
- The current work store is keyed by task and is not an immutable append-only verification ledger. Concurrent updates and runner failure recovery require further hardening.
- The verifier's checks and the reviewer execute sequentially in the same workspace. Stronger immutable snapshot isolation is required for fully independent execution.
- Legacy workspaces without captured base or WorkItem contracts are intentionally blocked in required mode.
- No merging or deployment is authorized by this configuration. Verify Docker/remote runtime sessions and real PostgreSQL transactions before promotion.
