"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");
const { loopbackUrl, signedToken } = require("./review-smoke.cjs");

test("review smoke only accepts uncredentialed loopback HTTP origins", () => {
  assert.equal(loopbackUrl("http://127.0.0.1:5001", "API"), "http://127.0.0.1:5001");
  assert.equal(loopbackUrl("http://localhost:5002", "WEB"), "http://localhost:5002");
  for (const invalid of [
    "https://example.com", "http://10.10.1.2:5001",
    "http://127.0.0.1:5001/something", "http://127.0.0.1:5001/?token=x",
    "http://user:password@localhost:5001", "file:///etc/passwd",
  ]) {
    assert.throws(() => loopbackUrl(invalid, "API"));
  }
});

test("synthetic JWTs are signed with a supplied test secret and are short lived", () => {
  const token = signedToken({
    org: "10000000-0000-4000-8000-000000000001",
    user: "20000000-0000-4000-8000-000000000001",
    email: "alpha@example.invalid",
  }, "isolated-smoke-test-secret-not-used-in-production");
  const parts = token.split(".");
  assert.equal(parts.length, 3);
  const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString());
  assert.equal(payload.iss, "dev-plane");
  assert.equal(payload.aud, "dev-plane-api");
  assert.equal(payload.user_id, "20000000-0000-4000-8000-000000000001");
  assert(payload.exp - payload.iat <= 930);
});
