import assert from "node:assert";
import { describe, it } from "node:test";
import { getOAuthStartRedirect } from "./oauth-bridge";

const url =
  "https://github.com/login/oauth/authorize?client_id=qa-client&state=qa-state";

function upstream(
  location = url,
  cookie = "oauth_state=qa-state; Path=/; Max-Age=600; HttpOnly; SameSite=Lax",
  status = 307,
) {
  return new Response(null, {
    status,
    headers: { Location: location, "Set-Cookie": cookie },
  });
}

describe("getOAuthStartRedirect", () => {
  it("extracts a GitHub OAuth redirect and its matching state cookie", () => {
    const { destination, stateCookie } = getOAuthStartRedirect(upstream());
    assert.strictEqual(destination.toString(), url);
    assert.match(stateCookie, /^oauth_state=qa-state;/);
    assert.match(stateCookie, /HttpOnly/);
  });

  it("rejects redirects off GitHub's authorization endpoint", () => {
    assert.throws(() =>
      getOAuthStartRedirect(upstream("https://attacker.invalid/phish")),
    );
    assert.throws(() =>
      getOAuthStartRedirect(
        upstream("http://github.com/login/oauth/authorize?state=qa-state"),
      ),
    );
    assert.throws(() =>
      getOAuthStartRedirect(
        upstream("https://github.com/other?state=qa-state"),
      ),
    );
  });

  it("rejects a state cookie that does not match GitHub's state query", () => {
    assert.throws(() =>
      getOAuthStartRedirect(
        upstream(
          url,
          "oauth_state=attacker-state; Path=/; HttpOnly; SameSite=Lax",
        ),
      ),
    );
    assert.throws(() =>
      getOAuthStartRedirect(
        upstream(url, "session=qa-state; Path=/; HttpOnly"),
      ),
    );
    assert.throws(() =>
      getOAuthStartRedirect(
        upstream(url, "oauth_state=qa-state; Path=/; SameSite=Lax"),
      ),
    );
    assert.throws(() =>
      getOAuthStartRedirect(
        upstream(
          url,
          "oauth_state=qa-state; Path=/; HttpOnly; SameSite=Strict",
        ),
      ),
    );
  });

  it("rejects an OAuth backend that did not return a 307", () => {
    assert.throws(() =>
      getOAuthStartRedirect(
        upstream(
          url,
          "oauth_state=qa-state; Path=/; HttpOnly; SameSite=Lax",
          200,
        ),
      ),
    );
  });
});
