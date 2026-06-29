/**
 * Lightweight, client-safe JWT payload decoder. It does NOT verify the
 * signature; the API is responsible for validation. Used by the web app to
 * bootstrap auth state from the stored bearer token.
 */

export interface TokenClaims {
  user_id: string;
  org_id: string;
  email: string;
  role: string;
  name?: string;
  avatar_url?: string;
  exp?: number;
}

export function decodeTokenClaims(token: string): TokenClaims | null {
  try {
    const parts = token.split(".");
    if (parts.length !== 3) {
      return null;
    }

    const payload = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const padding = (4 - (payload.length % 4)) % 4;
    const padded = payload.padEnd(payload.length + padding, "=");
    const json = atob(padded);
    return JSON.parse(json) as TokenClaims;
  } catch {
    return null;
  }
}
