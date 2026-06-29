import assert from "node:assert";
import { describe, it } from "node:test";
import { decodeTokenClaims } from "./auth-token";

function base64UrlEncode(value: unknown): string {
  const json = JSON.stringify(value);
  const buffer = Buffer.from(json, "utf8");
  return buffer.toString("base64url");
}

describe("decodeTokenClaims", () => {
  it("decodes a valid JWT payload", () => {
    const header = base64UrlEncode({ alg: "HS256", typ: "JWT" });
    const payload = base64UrlEncode({
      user_id: "user-123",
      org_id: "org-456",
      email: "dev@example.com",
      role: "owner",
      name: "Dev User",
    });
    const token = `${header}.${payload}.signature`;

    const claims = decodeTokenClaims(token);

    assert.ok(claims);
    assert.strictEqual(claims.user_id, "user-123");
    assert.strictEqual(claims.org_id, "org-456");
    assert.strictEqual(claims.email, "dev@example.com");
    assert.strictEqual(claims.role, "owner");
    assert.strictEqual(claims.name, "Dev User");
  });

  it("returns null for malformed tokens", () => {
    assert.strictEqual(decodeTokenClaims("not-a-jwt"), null);
    assert.strictEqual(decodeTokenClaims("only-one-part"), null);
    assert.strictEqual(decodeTokenClaims("two.parts"), null);
  });

  it("returns null for invalid base64 payload", () => {
    assert.strictEqual(decodeTokenClaims("header.!!!.sig"), null);
  });
});
