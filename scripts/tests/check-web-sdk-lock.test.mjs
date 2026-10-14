import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve, dirname } from "node:path";
import { test } from "node:test";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const readJSON = (path) => JSON.parse(readFileSync(resolve(root, path), "utf8"));

test("web SDK lock entry points to the checked-in directory rather than a generated tarball", () => {
  const manifest = readJSON("apps/web/package.json");
  const lock = readJSON("apps/web/package-lock.json");
  const sdk = readJSON("packages/sdk/typescript/package.json");
  const expected = "file:../../packages/sdk/typescript";
  const key = "@ai-cp/dev-plane-sdk";
  const resolved = lock.packages["node_modules/" + key];

  assert.equal(sdk.name, key);
  assert.equal(manifest.dependencies[key], expected);
  assert.equal(lock.packages[""].dependencies[key], expected);
  assert.ok(resolved, "local SDK lock entry must exist");
  assert.equal(resolved.version, sdk.version);
  assert.equal(resolved.resolved, expected, "stale archive paths cannot work in a clean checkout");
  assert.equal(resolved.integrity, undefined, "a local directory must not pin a missing tarball hash");
  assert.equal(
    readFileSync(resolve(root, "apps/web/.npmrc"), "utf8").trim(),
    "install-links=true",
    "SDK is installed from the built local directory as a package",
  );
});
