# Review UI: isolated staging QA

This procedure exercises the real Go API, migrated PostgreSQL, and a **production-built** Next.js UI using two synthetic organizations. It is a repeatable, localhost-only integration smoke, **not** a public staging deployment, GitHub OAuth certification, or release authorization.

Tracked release work: [#235](https://github.com/dporkka/dev-plane/pull/235) (review UI), [#236](https://github.com/dporkka/dev-plane/pull/236) (review inbox / metrics), [#239](https://github.com/dporkka/dev-plane/issues/239) (remote staging), [#212](https://github.com/dporkka/dev-plane/issues/212) (protected main / independent reviewer), and [#237](https://github.com/dporkka/dev-plane/issues/237) (multi-organization membership).

## Run

Prerequisites: Linux, a non-root account, Go 1.26+, Node/npm, PostgreSQL development binaries (`initdb`, `pg_ctl`, `createdb`, `psql`), `pg_config`, `openssl`, `curl`, and Chromium managed by Playwright. Install the matching browser binary once:

```sh
cd scripts/staging
npm ci
npx playwright install chromium
cd ../..
```

Run from a checked-out review branch (preferably an immutable, clean commit):

```sh
bash scripts/staging/run-local-review.sh
```

The script creates a fresh ephemeral PostgreSQL cluster/database, generates a random test-only JWT signing key, builds the API and SDK/web, binds both HTTP listeners **only to 127.0.0.1**, and seeds precisely two fictional organizations. It runs `review-smoke.cjs` against the actual services and prints a PASS/FAIL result. On success or failure it terminates the spawned processes, stops PostgreSQL, and deletes the temporary files and key.

To avoid conflicts, optional ports are `STAGING_DB_PORT`, `STAGING_API_PORT`, and `STAGING_WEB_PORT` (defaults: 55461, 56381, 56382). Set these to distinct unused local ports if needed. No production database URL or remote target can be supplied.

The script refuses to run as root or when application `.env` files are present. The API starts in a newly created working directory containing only copied migrations, with a sanitized environment and no GitHub or cloud credentials. The browser test only accepts uncredentialed HTTP loopback origins. Playwright reuses **browser binaries only**, never browser profiles or existing authenticated sessions.

## Coverage

| Check | Expected outcome |
| --- | --- |
| PostgreSQL migrations | All current migrations apply to an empty test database |
| Real JWT validation | Two locally signed, short-lived JWTs accepted; missing and expired JWTs rejected |
| Authorization | Organization A cannot view B projects, PRs, reviews, or metrics; B cannot view A |
| Repository filtering | Selecting B's repository while scoped to A returns no review metrics |
| PR inbox and details | Each authenticated browser context sees only its own PR and findings |
| Analytics and CSV | Synthetic review severity counts and CSV contain only the scoped PR |
| Mobile navigation | Sidebar opens and closes without horizontal overflow |
| Direct PR URL | User B is denied direct access to A's PR detail page |
| Browser errors | No unhandled page errors |

Tests do **not** intercept or fake backend API responses; fixture rows live in the ephemeral migrated PostgreSQL database. GitHub OAuth itself is not exercised: the QA tokens are generated from an ephemeral secret and checked by the normal API authentication middleware. No reviewer approvals, GitHub merges, deployment changes, live customer data, or AI agent runs are performed.

## Interpreting results

A passing local smoke proves the selected **working tree** works with these synthetic fixtures on this machine. If the checkout is dirty, the runner explicitly warns that it cannot qualify the displayed Git SHA as the tested source. For release evidence, validate a clean exact commit, record that SHA, and run independent CI/security checks.

This does **not** establish a deployed browser session, a trusted external staging URL, production compatibility, real OAuth redirect/callback behavior, or multi-organization membership. Those require a designated staging host, a separate TLS-protected domain and GitHub OAuth application, isolated secrets and database, a real login flow, and independent review. Do not reuse a production OAuth client, GitHub App private key, live JWT secret, or database for staging.

The existing single-organization-per-user model means switching between organizations in one account is **not** a supported backend capability yet; tracked in #237. Do not label it released based on these two separate synthetic accounts.

## Remaining release gates

1. Provision the authorized **remote** staging web/API deployment and separate database, record exact SHA and sanitized evidence under #239.
2. Complete real OAuth login, logout, expiry, direct-link, mobile, and cross-tenant staging checks without test token injection.
3. Provide a genuinely independent human code/security reviewer; establish protected-main governance in #212. No agent-authored verdict counts as approval.
4. Resolve outstanding review requests and run required checks on the exact final PR heads, merging the parent #235 before the stacked child #236 only after all gates pass.

**Do not merge or deploy automatically because this smoke is green.**
