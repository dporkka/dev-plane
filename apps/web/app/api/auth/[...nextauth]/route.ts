import { NextRequest, NextResponse } from 'next/server';

const API_BASE = process.env.NEXT_PUBLIC_API_URL || 'http://localhost:8080';

/**
 * Custom GitHub OAuth handler for the Next.js app.
 *
 * Flow:
 * 1. The browser hits this route to initiate sign-in. We redirect to the Go
 *    backend's OAuth initiator, which sets an HttpOnly oauth_state cookie and
 *    redirects the user to GitHub.
 * 2. GitHub redirects back here with `code` and `state`. We forward those
 *    parameters (along with the state cookie) to the backend callback, which
 *    exchanges the code for a GitHub token, provisions the user/org, and
 *    returns a signed JWT.
 * 3. We render a tiny page that stores the JWT in localStorage and sends the
 *    user to the dashboard.
 */
export async function GET(req: NextRequest) {
  const { searchParams } = req.nextUrl;
  const code = searchParams.get('code');
  const state = searchParams.get('state');
  const token = searchParams.get('token');

  if (token) {
    return renderTokenCallback(token);
  }

  if (code && state) {
    const cookieHeader = req.headers.get('cookie') || '';
    const backendUrl = new URL(`${API_BASE}/api/v1/auth/github/callback`);
    backendUrl.searchParams.set('code', code);
    backendUrl.searchParams.set('state', state);

    const response = await fetch(backendUrl.toString(), {
      headers: { cookie: cookieHeader },
    });

    if (!response.ok) {
      const text = await response.text();
      return renderError(
        `GitHub sign-in failed: ${text || response.statusText}`
      );
    }

    const data = (await response.json()) as { token?: string };
    if (!data.token) {
      return renderError('GitHub sign-in did not return a token.');
    }

    return renderTokenCallback(data.token);
  }

  // Initiate GitHub OAuth through the backend.
  const redirectUri = `${req.nextUrl.origin}/api/auth/github/callback`;
  const url = new URL(`${API_BASE}/api/v1/auth/github`);
  url.searchParams.set('redirect_uri', redirectUri);
  return NextResponse.redirect(url);
}

export async function POST(req: NextRequest) {
  // Support NextAuth-style POST sign-in requests by treating them the same as
  // a GET to the route root.
  return GET(req);
}

function renderTokenCallback(token: string) {
  const html = `<!doctype html>
<html lang="en">
  <body>
    <p>Signing you in...</p>
    <script>
      (function () {
        localStorage.setItem('token', ${JSON.stringify(token)});
        window.location.replace('/dashboard');
      })();
    </script>
  </body>
</html>`;
  return new NextResponse(html, {
    headers: { 'Content-Type': 'text/html; charset=utf-8' },
  });
}

function renderError(message: string) {
  const safeMessage = message.replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const html = `<!doctype html>
<html lang="en">
  <body>
    <h1>Sign-in failed</h1>
    <p>${safeMessage}</p>
    <a href="/">Back home</a>
  </body>
</html>`;
  return new NextResponse(html, {
    status: 400,
    headers: { 'Content-Type': 'text/html; charset=utf-8' },
  });
}
