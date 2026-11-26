/**
 * Validate the server-side OAuth initiation response before converting it
 * into a same-origin browser redirect. We never accept the redirect destination
 * from browser input and never forward arbitrary upstream cookies.
 */
export function getOAuthStartRedirect(response: Response): {
  destination: URL;
  stateCookie: string;
} {
  if (response.status !== 307) {
    throw new Error("OAuth backend did not initiate authorization");
  }

  const location = response.headers.get("location");
  if (!location) {
    throw new Error("OAuth backend omitted authorization destination");
  }
  const destination = new URL(location);
  if (
    destination.protocol !== "https:" ||
    destination.hostname !== "github.com" ||
    destination.username !== "" ||
    destination.password !== "" ||
    destination.pathname !== "/login/oauth/authorize"
  ) {
    throw new Error("OAuth backend returned an unexpected destination");
  }

  const state = destination.searchParams.get("state");
  if (!state) {
    throw new Error("OAuth backend omitted CSRF state");
  }

  const cookies = response.headers.getSetCookie();
  const stateCookie = cookies.find((cookie) =>
    cookie.startsWith("oauth_state="),
  );
  if (!stateCookie) {
    throw new Error("OAuth backend omitted state cookie");
  }
  const match = /^oauth_state=([A-Za-z0-9_-]+)(?:;|$)/.exec(stateCookie);
  if (
    !match ||
    match[1] !== state ||
    !/(?:^|;)\s*httponly(?:;|$)/i.test(stateCookie) ||
    !/(?:^|;)\s*samesite=lax(?:;|$)/i.test(stateCookie)
  ) {
    throw new Error("OAuth backend returned an invalid state cookie");
  }

  return { destination, stateCookie };
}
