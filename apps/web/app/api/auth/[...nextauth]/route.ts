import { getOAuthStartRedirect } from "@/lib/oauth-bridge";
import { type NextRequest, NextResponse } from "next/server";

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";

/**
 * GitHub authorization code flow through the Next.js origin.
 *
 * The API has its own host in staging. The browser MUST NOT visit its OAuth
 * initiation endpoint directly, since that places the CSRF cookie on the API
 * host instead of this web host. We initiate server-to-server and forward
 * ONLY the validated oauth_state cookie with the GitHub redirect.
 *
 * Set GITHUB_OAUTH_REDIRECT_URL on the backend to this route's absolute URL.
 */
export async function GET(req: NextRequest) {
  const { searchParams } = req.nextUrl;
  const code = searchParams.get("code");
  const state = searchParams.get("state");

  // Never accept a bearer token in the query string. URLs are commonly
  // logged, included in browser history, and supplied by arbitrary callers.
  if (searchParams.has("token")) {
    return renderError("URL-based sign-in tokens are not supported.");
  }
  if (searchParams.has("error")) {
    return renderError("GitHub authorization was declined.");
  }

  // GitHub's callback is a cross-site top-level GET. The Lax state cookie
  // belongs to this web origin, so Next.js can forward only that cookie to Go.
  if (code !== null || state !== null) {
    const cookieState = req.cookies.get("oauth_state")?.value;
    if (!code || !state || !cookieState || cookieState !== state) {
      return renderError("Invalid or expired GitHub sign-in state.");
    }

    const backendUrl = new URL(`${API_BASE}/api/v1/auth/github/callback`);
    backendUrl.searchParams.set("code", code);
    backendUrl.searchParams.set("state", state);

    let response: Response;
    try {
      response = await fetch(backendUrl.toString(), {
        headers: { Cookie: `oauth_state=${encodeURIComponent(cookieState)}` },
        cache: "no-store",
        redirect: "manual",
      });
    } catch {
      return renderError("GitHub sign-in service is unavailable.", 502);
    }

    if (!response.ok) {
      return renderError("GitHub sign-in failed. Please try again.", 502);
    }
    let data: { token?: string };
    try {
      data = (await response.json()) as { token?: string };
    } catch {
      return renderError("GitHub sign-in response was invalid.", 502);
    }
    if (typeof data.token !== "string" || !data.token) {
      return renderError("GitHub sign-in did not return a token.", 502);
    }

    const result = renderTokenCallback(data.token);
    result.cookies.set({
      name: "oauth_state",
      value: "",
      path: "/",
      maxAge: 0,
      httpOnly: true,
      secure: req.nextUrl.protocol === "https:",
      sameSite: "lax",
    });
    return result;
  }

  // Initiation must stay on the web origin. The Go backend returns the GitHub
  // authorization redirect + a short-lived HttpOnly state cookie; we validate
  // that pair and bind the cookie to the web origin on this response.
  let upstream: Response;
  try {
    upstream = await fetch(`${API_BASE}/api/v1/auth/github`, {
      cache: "no-store",
      redirect: "manual",
    });
  } catch {
    return renderError("GitHub sign-in service is unavailable.", 502);
  }

  try {
    const { destination, stateCookie } = getOAuthStartRedirect(upstream);
    const redirect = NextResponse.redirect(destination, 307);
    redirect.headers.append("Set-Cookie", stateCookie);
    redirect.headers.set("Cache-Control", "no-store, max-age=0");
    redirect.headers.set("Referrer-Policy", "no-referrer");
    return redirect;
  } catch {
    return renderError("GitHub sign-in could not be started.", 502);
  }
}

export async function POST(req: NextRequest) {
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
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store, max-age=0",
      "Referrer-Policy": "no-referrer",
      "X-Content-Type-Options": "nosniff",
      "X-Frame-Options": "DENY",
    },
  });
}

function renderError(message: string, status = 400) {
  const safeMessage = message
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
  const html = `<!doctype html>
<html lang="en">
  <body>
    <h1>Sign-in failed</h1>
    <p>${safeMessage}</p>
    <a href="/">Back home</a>
  </body>
</html>`;
  return new NextResponse(html, {
    status,
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "no-store, max-age=0",
      "Referrer-Policy": "no-referrer",
      "X-Content-Type-Options": "nosniff",
    },
  });
}
