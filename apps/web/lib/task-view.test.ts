import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { TASK_BOARD_COLUMNS, filterTasks, isNeedsAttention } from "./task-view";
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
