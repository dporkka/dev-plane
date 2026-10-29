import type { Priority, RiskLevel, Task, TaskStatus } from "./types";

export const TASK_BOARD_COLUMNS: ReadonlyArray<{
  status: TaskStatus;
  title: string;
}> = [
  { status: "backlog", title: "Backlog" },
  { status: "spec_review", title: "Spec Review" },
  { status: "approved", title: "Approved" },
  { status: "running", title: "Running" },
  { status: "reviewing", title: "Reviewing" },
  { status: "pr_created", title: "PR Created" },
  { status: "deploying", title: "Deploying" },
  { status: "done", title: "Done" },
  { status: "failed", title: "Failed" },
  { status: "cancelled", title: "Cancelled" },
];

export type TaskScope = "all" | "needs_attention";

export interface TaskFilters {
  query: string;
  status: TaskStatus | "all";
  repositoryId: string | "all";
  priority: Priority | "all";
  risk: RiskLevel | "all";
  scope: TaskScope;
}

export const DEFAULT_TASK_FILTERS: TaskFilters = {
  query: "",
  status: "all",
  repositoryId: "all",
  priority: "all",
  risk: "all",
  scope: "all",
};

// Keep this narrow: additional signals such as pending approvals or blocked
// dependencies must come from verified server data, not inferred from age/risk.
export function isNeedsAttention(task: Pick<Task, "status">): boolean {
  return task.status === "failed" || task.status === "spec_review";
}

export function filterTasks(
  tasks: readonly Task[],
  filters: TaskFilters,
): Task[] {
  const query = filters.query.trim().toLowerCase();
  return tasks.filter((task) => {
    if (filters.scope === "needs_attention" && !isNeedsAttention(task)) {
      return false;
    }
    if (filters.status !== "all" && task.status !== filters.status) {
      return false;
    }
    if (
      filters.repositoryId !== "all" &&
      task.repository_id !== filters.repositoryId
    ) {
      return false;
    }
    if (filters.priority !== "all" && task.priority !== filters.priority) {
      return false;
    }
    if (filters.risk !== "all" && task.risk_level !== filters.risk) {
      return false;
    }
    return (
      !query ||
      task.title.toLowerCase().includes(query) ||
      (task.description ?? "").toLowerCase().includes(query)
    );
  });
}
