import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");

test("Go lint Makefile expands shell loop variables and retains fail-closed exit", () => {
  const result = spawnSync("make", ["-n", "lint-go", "GO_PACKAGES=packages/agent-runtime", "GO_APPS="], {
    cwd: root,
    encoding: "utf8",
    timeout: 60_000,
  });
  assert.equal(result.status, 0, result.stderr);
  const plan = result.stdout;
  assert.match(plan, /for mod in packages\/agent-runtime\s*; do/);
  assert.match(plan, /echo "  --> \$mod"/);
  assert.match(plan, /cd "\$mod" && golangci-lint run --config/);
  assert.match(plan, /if \[ "\$failed" -ne 0 \]; then/);
  assert.match(plan, /exit 1;/);
  assert.doesNotMatch(plan, /cd "od"/);

  const ratchet = spawnSync("make", ["-n", "lint-go", "GO_PACKAGES=packages/agent-runtime", "GO_APPS=", "LINT_BASE_REV=abc123"], {
    cwd: root,
    encoding: "utf8",
    timeout: 60_000,
  });
  assert.equal(ratchet.status, 0, ratchet.stderr);
  assert.match(ratchet.stdout, /--new-from-rev="abc123" --whole-files/);
  assert.match(ratchet.stdout, /if \[ "\$failed" -ne 0 \]; then/);
});
