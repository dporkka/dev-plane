import assert from "node:assert/strict";
import test from "node:test";

import type { ApprovalType } from "./openapi-types";

test("OpenAPI approval contract includes pr_create", () => {
  const approvalType: ApprovalType = "pr_create";
  assert.equal(approvalType, "pr_create");
});
