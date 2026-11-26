# GitHub OAuth staging: two-origin web/API setup

Dev Plane uses a Next.js web app and a Go API. GitHub's authorization-code
callback must return to the **web origin**, not the API origin. This is especially
important when staging uses separate web and API domains.

The web route at `/api/auth/github/callback` initiates the OAuth flow
server-to-server, validates the Go API's GitHub redirect and CSRF state, then
sets the short-lived `oauth_state` HttpOnly cookie on the **web origin**.
GitHub returns to that same web-origin route. The web route verifies and
forwards **only** that state cookie and the authorization code to Go. It then
clears the state cookie and returns a non-cacheable JWT handoff page.

## Required setup

1. Use a dedicated **staging GitHub OAuth app**, not production credentials.
   Register an exact callback, for example
   `https://review-staging.example.com/api/auth/github/callback`.
2. Deploy the **web** and **Go API** to separate, designated nonproduction
   services with TLS. Keep staging PostgreSQL and application secrets isolated
   from production and other projects.
3. Configure the **Go API** via provider-managed staging secrets/variables:

   - `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET`: staging OAuth app only
   - `GITHUB_OAUTH_REDIRECT_URL`: exact web-origin callback URL above
   - `OAUTH_COOKIE_SECURE=true`
   - `JWT_SECRET`: a new staging-only signing key of at least 32 characters
   - `ALLOWED_ORIGINS`: exact staging web origin
   - `DATABASE_URL`: dedicated staging PostgreSQL, never a production DSN

4. Configure the **web** with `NEXT_PUBLIC_API_URL` as the staging API
   origin. Build and deploy the web app with this value. Ensure that
   `/api/auth/github/callback` is handled by Next.js, while only
   `/api/v1/*` is proxied to Go by `next.config.js`.
5. Confirm the OAuth app's callback URL and Go's
   `GITHUB_OAUTH_REDIRECT_URL` match byte-for-byte, including HTTPS.
   The backend rejects off-site HTTP callbacks, query strings, credentials,
   and paths outside `/api/auth/github/callback`.

**No client secrets belong in GitHub issue comments, browser logs, source
control, screenshots, or workflow artifacts.** The frontend receives a signed
application JWT only after the backend verifies GitHub identity.

## Local regression verification

The Go tests check the state cookie's `SameSite=Lax` (needed for GitHub's
top-level cross-site callback), the explicit trusted callback URI, and rejection
of unsafe configuration.

```sh
cd apps/api && go test ./internal/config ./internal/handlers
```

The web unit tests reject non-GitHub redirects, missing/mismatched state
cookies, insecure cookie attributes, and failed initiation responses.

```sh
cd apps/web && npm test
```

The black-box smoke launches a production-built Next.js server and a local
**synthetic** Go-like OAuth API on loopback. Build with the specified test API:

```sh
cd apps/web
NEXT_PUBLIC_API_URL=http://127.0.0.1:56521 npm run build
cd ../..
node scripts/tests/oauth-web-bridge-smoke.mjs
```

The smoke verifies web-origin cookie forwarding, callback state checking,
cookie clearing, non-cacheable token response, and token-in-URL rejection.
It does **not** contact GitHub, authenticate a real GitHub identity, or
exercise user/org provisioning in the Go API.

## Remote release evidence still required

- Staging OAuth app registration and manual browser authorization against
  real GitHub, including approval/decline, sign-out, expired state and login
  on desktop/mobile
- Authenticated database-backed tenancy isolation using multiple actual
  staging test identities
- HTTPS origin, cookie and redirect verification, and no access to production
  data or credentials
- Independent security review of the underlying OAuth **identity-to-membership
  binding** (tracked separately from the cookie transport changes)
- Protected-main checks and one genuinely independent reviewer before merge

These source-level fixes do not themselves create or deploy a remote staging
project, and they do not imply the outstanding release gates have passed.
