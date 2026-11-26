# GitHub OAuth identity-binding rollout (dev-plane)

The OAuth handler introduced by `fix/security-oauth-membership-binding-20261120` resolves users from the immutable GitHub numeric subject and the **persisted** active organization and role. Before this change, the callback selected organizations by the mutable GitHub login and issued an unconditional owner role.

## Mandatory preflight (stop on any failure)

1. Back up the identity tables and verify recovery on a disposable database.
2. Run this read-only query against the deployment database:

   ```sql
   SELECT github_id, COUNT(*) AS rows, COUNT(DISTINCT organization_id) AS organizations
   FROM users
   WHERE github_id IS NOT NULL
   GROUP BY github_id
   HAVING COUNT(*) > 1;
   ```

   **Expected result: zero rows.** Do not automatically delete, deduplicate, or reassign entries. Manually verify external subjects and membership before migration.
3. Inventory active, soft-deleted, and legacy Github-linked users and verify the organization memberships and roles before making any change. Check any historical data that stores the username as the organization slug; this patch **does not** change existing organization slugs.
4. Verify the additive uniqueness migration `025_unique_github_provider_subject.sql` succeeds, including the production SQL dialect, before admitting login traffic.
5. Verify the new handler on a disposable staging database with account rename, deleted account/organization, a non-owner role, duplicate IDs, concurrent first sign-in, and a renamed/reused login.

## Coordinated deployment

- **Do not use an ordinary rolling/mixed-version OAuth deployment.** Old replicas still derive tenants from the mutable username and issue owner claims. Mixed-version traffic may disagree about the same user.
- Temporarily stop **OAuth callback admission** at the entrypoint; leave unrelated application traffic available if safe. Deploy the migration and update **all** login-serving replicas, verify their version and live route ownership, then reopen the callback.
- If the unique index fails because of historical duplicate subjects, leave login admission disabled until the records can be independently reconciled. Do not weaken uniqueness or allow a fallback to username binding.
- A newly provisioned account receives an organization slug of `github-user-<numeric GitHub ID>`. Do not change this back to a user-controlled login name for compatibility.

## Rollback and limitations

- **A binary rollback to the previous OAuth callback is unsafe once new accounts exist.** That handler can mint an organization claim based on the current username rather than the persisted membership. Disable the affected login callback and forward-fix, or perform a reviewed identity-data reconciliation before any rollback.
- This change only corrects **new JWT issuance**; a previously issued JWT can still contain stale role/organization claims until expiration. Token revocation and live request authorization need a separate security change before strong revocation guarantees can be claimed.
- This patch accepts an address from GitHub's public `/user` response when present; only the fallback `/user/emails` path verifies the primary email. Do not use the email as an authorization key. Further email-verification hardening is separate.
- Never use a production database or live customer identity data for local tests. Record the migration version, staged evidence, deployed artifact digest, and rollback decision.
