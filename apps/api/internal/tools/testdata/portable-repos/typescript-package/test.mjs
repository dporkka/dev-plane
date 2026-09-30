import test from "node:test";
import assert from "node:assert/strict";

test("fixture is dependency free", () => {
  assert.equal(2 + 3, 5);
});
