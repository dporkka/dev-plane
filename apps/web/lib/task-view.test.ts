import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  TASK_BOARD_COLUMNS,
  buildTaskDependencyIndex,
  filterTasks,
  isNeedsAttention,
} from "./task-view";
import type { Task } from "./types";

function task(
  id: string,
  status: Task["status"],
  title: string,
  overrides: Partial<Task> = {},
): Task {
  return {
    id,
    status,
    title,
    description: "",
    project_id: "project-1",
    repository_id: "repo-1",
    created_by: "user-1",
    source: "web",
    priority: "medium",
    risk_level: "low",
    target_branch: "main",
    max_runtime_minutes: 60,
    created_at: "2026-10-28T00:00:00Z",
    updated_at: "2026-10-28T00:00:00Z",
    ...overrides,
  };
}

describe("task views", () => {
  it("shows every backend lifecycle state, including deploying, failed and cancelled", () => {
    assert.deepEqual(
      TASK_BOARD_COLUMNS.map((column) => column.status),
      [
        "backlog",
        "spec_review",
        "approved",
        "running",
        "reviewing",
        "pr_created",
        "deploying",
        "done",
        "failed",
        "cancelled",
      ],
    );
  });

  it("only flags actionable failure or spec review as needing attention", () => {
    assert.equal(isNeedsAttention(task("1", "failed", "Failed")), true);
    assert.equal(isNeedsAttention(task("2", "spec_review", "Spec")), true);
    assert.equal(isNeedsAttention(task("3", "deploying", "Deploying")), false);
    assert.equal(isNeedsAttention(task("4", "cancelled", "Cancelled")), false);
  });

  it("filters task text case-insensitively, repository, priority, risk and status", () => {
    const tasks = [
      task("1", "failed", "Database migration", {
        description: "Fix PostgreSQL",
        repository_id: "repo-2",
        priority: "high",
        risk_level: "critical",
      }),
      task("2", "running", "Cache cleanup"),
    ];
    assert.deepEqual(
      filterTasks(tasks, {
        query: "POSTGRESql",
        repositoryId: "repo-2",
        priority: "high",
        risk: "critical",
        status: "failed",
        scope: "all",
      }).map((item) => item.id),
      ["1"],
    );
  });

  it("returns only actionable tasks for Needs Attention without changing source", () => {
    const tasks = [
      task("1", "failed", "Broken"),
      task("2", "spec_review", "Approve spec"),
      task("3", "done", "Done"),
    ];
    const filtered = filterTasks(tasks, {
      query: "",
      repositoryId: "all",
      priority: "all",
      risk: "all",
      status: "all",
      scope: "needs_attention",
    });
    assert.deepEqual(
      filtered.map((item) => item.id),
      ["1", "2"],
    );
    assert.equal(tasks.length, 3);
  });
});

describe("scheduler dependency snapshots", () => {
  it("uses the canonical metadata scheduler depends_on and done status", () => {
    const tasks = [
      task("ready", "done", "Foundation"),
      task("running", "running", "Build API"),
      task("blocked", "approved", "Deploy", {
        metadata: {
          scheduler: { depends_on: ["ready", "running", "running"] },
        },
      }),
    ];
    const index = buildTaskDependencyIndex(tasks);
    const view = index.get("blocked");
    assert.ok(view);
    assert.deepEqual(
      view.dependencies.map(({ state, title }) => [state, title]),
      [
        ["complete", "Foundation"],
        ["waiting", "Build API"],
      ],
    );
    assert.equal(view.blocked, true);
    assert.equal(view.needsIntervention, false);
    assert.deepEqual(
      index.get("running")?.dependents.map(({ id }) => id),
      ["blocked"],
    );
  });

  it("does not leak unknown or other-project dependency task details", () => {
    const tasks = [
      task("private", "failed", "Secret task", { project_id: "other-project" }),
      task("consumer", "approved", "Public task", {
        metadata: { scheduler: { depends_on: ["private", "missing"] } },
      }),
    ];
    const view = buildTaskDependencyIndex(tasks).get("consumer");
    assert.ok(view);
    assert.deepEqual(
      view.dependencies.map(({ state, title, id }) => [state, title, id]),
      [
        ["unavailable", "Not visible in this project", undefined],
        ["unavailable", "Not visible in this project", undefined],
      ],
    );
    assert.equal(view.needsIntervention, true);
  });

  it("flags failed prerequisites and malformed scheduler fields for triage", () => {
    const tasks = [
      task("failed-dep", "failed", "Failing check"),
      task("consumer", "approved", "Consumer", {
        metadata: { scheduler: { depends_on: ["failed-dep"] } },
      }),
      task("invalid", "backlog", "Invalid config", {
        metadata: { scheduler: { depends_on: "failed-dep" } },
      }),
    ];
    const index = buildTaskDependencyIndex(tasks);
    assert.equal(index.get("consumer")?.dependencies[0]?.state, "failed");
    assert.equal(index.get("consumer")?.needsIntervention, true);
    assert.equal(index.get("invalid")?.dependencies[0]?.state, "invalid");
    assert.equal(index.get("invalid")?.needsIntervention, true);
    assert.equal(isNeedsAttention(tasks[1], index.get("consumer")), true);
    assert.equal(isNeedsAttention(tasks[2], index.get("invalid")), true);
  });

  it("filters by dependency state from the full project snapshot", () => {
    const tasks = [
      task("a", "running", "Dependency"),
      task("b", "approved", "Waiting", {
        metadata: { scheduler: { depends_on: ["a"] } },
      }),
      task("c", "done", "Completed"),
    ];
    const index = buildTaskDependencyIndex(tasks);
    assert.deepEqual(
      filterTasks(
        tasks,
        { ...DEFAULT_TASK_FILTERS, dependency: "blocked" },
        index,
      ).map((item) => item.id),
      ["b"],
    );
    assert.deepEqual(
      filterTasks(
        tasks,
        { ...DEFAULT_TASK_FILTERS, dependency: "needs_intervention" },
        index,
      ).map((item) => item.id),
      [],
    );
  });

  it("handles a self-reference as invalid rather than falsely ready", () => {
    const tasks = [
      task("self", "approved", "Self", {
        metadata: { scheduler: { depends_on: ["self"] } },
      }),
    ];
    const view = buildTaskDependencyIndex(tasks).get("self");
    assert.equal(view?.dependencies[0]?.state, "invalid");
    assert.equal(view?.blocked, true);
  });
});
