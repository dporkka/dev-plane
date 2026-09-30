import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { getAdmissionEvidence } from "./readiness-evidence";

describe("getAdmissionEvidence", () => {
  test("returns persisted admission readiness and lineage", () => {
    const evidence = getAdmissionEvidence({
      admission: {
        policy: "task-readiness-v1",
        readiness: {
          status: "attention",
          checks: [
            {
              id: "scope",
              title: "Bounded file scope",
              status: "attention",
              critical: false,
              recommendation: "Identify expected files.",
            },
          ],
        },
      },
      retry: { original_run_id: "run-parent" },
    });

    assert.equal(evidence?.policy, "task-readiness-v1");
    assert.equal(evidence?.readiness.status, "attention");
    assert.equal(evidence?.retryOriginalRunId, "run-parent");
  });

  test("returns handoff lineage", () => {
    const evidence = getAdmissionEvidence({
      admission: {
        policy: "task-readiness-v1",
        readiness: { status: "ready", checks: [] },
      },
      trigger: "mailbox_handoff",
      handoff_from_run_id: "run-previous",
      handoff_from_agent: "implementer",
    });

    assert.equal(evidence?.trigger, "mailbox_handoff");
    assert.equal(evidence?.handoffFromRunId, "run-previous");
    assert.equal(evidence?.handoffFromAgent, "implementer");
  });

  test("rejects missing or malformed admission evidence", () => {
    assert.equal(getAdmissionEvidence(undefined), null);
    assert.equal(getAdmissionEvidence({}), null);
    assert.equal(
      getAdmissionEvidence({
        admission: {
          policy: "task-readiness-v1",
          readiness: { status: "unknown", checks: [] },
        },
      }),
      null,
    );
  });
});
