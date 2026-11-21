import assert from "node:assert/strict";
import test from "node:test";

test("task navigation uses an HTTP redirect to existing project selection", async () => {
  // A server-config redirect works without client hydration or JavaScript.
  const config = require("../next.config.js");
  assert.equal(typeof config.redirects, "function");
  const redirects = await config.redirects();
  assert.deepEqual(
    redirects.find((entry: { source: string }) => entry.source === "/tasks"),
    { source: "/tasks", destination: "/projects", permanent: false },
  );
});
