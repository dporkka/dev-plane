# External GitHub PR triage inbox

Dev Plane can display **advisory, imported PR Triage snapshots** alongside
its agent-generated work. This is a first, pull-based integration and is not
a live GitHub mirror, automated reviewer, or merge authorization system.

## Setup

1. Apply Dev Plane migrations, including `026_external_pr_triage.sql`.
2. Connect the relevant GitHub repositories to projects in the desired Dev
   Plane organization (the import rejects unconnected or ambiguous repositories).
3. On your trusted local machine run your existing PR Triage scanner:

   ```sh
   prtriage scan --detail-limit 80
   ```

4. From that machine import the chmod-0600 snapshot with a Dev Plane JWT that
   belongs to a **currently verified owner/admin** of the organization:

   ```sh
   : "${DEV_PLANE_API_BASE:?Set Dev Plane API URL}"
   : "${DEV_PLANE_ORG_ID:?Set organization ID}"
   : "${DEV_PLANE_TOKEN:?Set authenticated Dev Plane JWT}"

   curl --fail-with-body --silent --show-error \
     -X POST \
     -H "Authorization: Bearer ${DEV_PLANE_TOKEN}" \
     -H 'Content-Type: application/json' \
     --data-binary @"/home/${USER}/.local/share/pr-triage/snapshot.json" \
     "${DEV_PLANE_API_BASE%/}/api/v1/organizations/${DEV_PLANE_ORG_ID}/pr-triage/import"
   ```

   When the scan contains more than 500 PRs, use `jq` to import bounded
   batches without discarding the original snapshot:

   ```sh
   jq -c '. as $src | range(0; ($src.decisions | length); 500) as $start
     | {generated_at: $src.generated_at, warnings: $src.warnings,
        decisions: $src.decisions[$start:($start + 500)]}' \
     "$HOME/.local/share/pr-triage/snapshot.json" |
   while IFS= read -r batch; do
     printf '%s' "$batch" | curl --fail-with-body --silent --show-error \
       -X POST -H "Authorization: Bearer ${DEV_PLANE_TOKEN}" \
       -H 'Content-Type: application/json' --data-binary @- \
       "${DEV_PLANE_API_BASE%/}/api/v1/organizations/${DEV_PLANE_ORG_ID}/pr-triage/import" ||
       exit 1
   done
   ```

   Don't put API tokens in logs, repository files, shell history, or web pages.
   Do not expose PR Triage's unauthenticated localhost-only HTTP service.

5. Open **PR triage** in Dev Plane's sidebar
   (`/reviews/triage`). Filter by bucket or exact repository name and inspect
   GitHub links. The "Refresh cache" button rereads Dev Plane, **not GitHub**.

The API also supports:

```http
GET /api/v1/organizations/{orgID}/pr-triage?limit=50&offset=0&bucket=review&repo=owner/repo
POST /api/v1/organizations/{orgID}/pr-triage/import
```

Both routes require Dev Plane authentication and current organization membership;
import also requires owner/admin status confirmed against the current database.
Each list query joins the live repository/project ownership, so disconnecting a
repository hides its cached records. Import is bounded to 2 MiB and 500 PR
decisions; larger scans should be chunked into bounded, recent snapshots.

## Trust and completeness boundaries

- Import never creates repositories, changes GitHub state, runs CI, or merges PRs.
- The source snapshot (including its `candidate` and `verified` fields) is **not
  authenticated GitHub or CI execution evidence**. Do not use this table or API
  to decide whether a PR can be merged.
- The imported cache may contain **partial inventory**, because PR Triage
  scans/enriches within its configured limits. The API advertises
  `completeness_unverified` and does not fabricate a total PR count.
- Imports never delete records omitted from a later scan; this prevents
  accidental disappearance with partial scans. Records can therefore outlive
  closed/merged PRs. The UI marks snapshot evidence older than 24 hours as
  stale. No automatic closure or merge conclusions should be derived from it.
- If a newer snapshot for the same PR is already imported, an older one will
  not overwrite it. A successful HTTP import response reports the actual
  updated count and scanner warning count.
- The current UI lists cached decisions only. CodeRabbit/PR-Agent/Reviewdog
  findings and Woodpecker/Dagger evidence require separate authenticated
  adapters and explicit provenance checks before they can be displayed
  as current verification.

## Follow-up design

When Dev Plane's unified review inbox stack (#235/#236) merges, adopt this
organization-scoped external source as an additional dataset rather than
overwriting its task-owned `pull_requests` rows. Prefer eventually replacing
the manual import with a signed GitHub webhook + reconciliation worker and a
bounded durable queue. Do not invoke the scanner from an API request.
