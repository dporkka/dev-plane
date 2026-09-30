import assert from "node:assert";
import { describe, it } from "node:test";
import type { AgentRun } from "./types";
import { buildExecutionGraph } from "./execution-graph";

function run(overrides: Partial<AgentRun> & Pick<AgentRun, "id">): AgentRun {
  return {
    id: overrides.id,
    task_id: "task-1",
    attempt: 1,
    agent_role: "implementer",
    status: "completed",
    prompt_tokens: 0,
    completion_tokens: 0,
    total_cost: 0,
    created_at: "2026-09-30T12:00:00Z",
    updated_at: "2026-09-30T12:01:00Z",
    ...overrides,
  };
}

describe("buildExecutionGraph", () => {
  it("builds parent-child lineage and labels review repairs", () => {
    const graph = buildExecutionGraph([
      run({ id: "root", outcome: "failed" }),
      run({
        id: "repair",
        parent_run_id: "root",
        attempt: 2,
        metadata: { trigger: "review_repair", repair_round: 1 },
        outcome: "passed",
        total_cost: 0.75,
      }),
      run({
        id: "reviewer",
        parent_run_id: "repair",
        attempt: 3,
        agent_role: "reviewer",
        metadata: { trigger: "mailbox_handoff" },
        outcome: "passed",
        total_cost: 0.25,
      }),
    ]);

    assert.strictEqual(graph.nodes.length, 3);
    assert.deepStrictEqual(
      graph.nodes.map((node) => [node.id, node.depth]),
      [
        ["root", 0],
        ["repair", 1],
        ["reviewer", 2],
      ],
    );
    assert.deepStrictEqual(
      graph.edges.map((edge) => [edge.source, edge.target, edge.label]),
      [
        ["root", "repair", "repair #1"],
        ["repair", "reviewer", "handoff"],
      ],
    );
    assert.strictEqual(graph.summary.repairs, 1);
    assert.strictEqual(graph.summary.passed, 2);
    assert.strictEqual(graph.summary.failed, 1);
    assert.strictEqual(graph.summary.totalCost, 1);
  });

  it("keeps orphaned runs visible as roots", () => {
    const graph = buildExecutionGraph([
      run({ id: "orphan", parent_run_id: "missing", attempt: 4 }),
    ]);

    assert.strictEqual(graph.nodes.length, 1);
    assert.strictEqual(graph.nodes[0].depth, 0);
    assert.strictEqual(graph.nodes[0].orphaned, true);
    assert.strictEqual(graph.edges.length, 0);
  });

  it("does not recurse forever when lineage contains a cycle", () => {
    const graph = buildExecutionGraph([
      run({ id: "a", parent_run_id: "b" }),
      run({ id: "b", parent_run_id: "a", attempt: 2 }),
    ]);

    assert.strictEqual(graph.nodes.length, 2);
    assert.ok(graph.nodes.every((node) => Number.isFinite(node.depth)));
    assert.strictEqual(graph.summary.total, 2);
  });

  it("summarizes active and error outcomes separately", () => {
    const graph = buildExecutionGraph([
      run({ id: "running", status: "running", outcome: undefined }),
      run({ id: "error", status: "failed", outcome: "error" }),
      run({ id: "cancelled", status: "cancelled", outcome: "cancelled" }),
    ]);

    assert.strictEqual(graph.summary.active, 1);
    assert.strictEqual(graph.summary.errors, 1);
    assert.strictEqual(graph.summary.cancelled, 1);
  });
});
