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
export type DependencyFilter = "all" | "blocked" | "needs_intervention";

export interface TaskFilters {
  query: string;
  status: TaskStatus | "all";
  repositoryId: string | "all";
  priority: Priority | "all";
  risk: RiskLevel | "all";
  scope: TaskScope;
  dependency?: DependencyFilter;
}

export const DEFAULT_TASK_FILTERS: TaskFilters = {
  query: "",
  status: "all",
  repositoryId: "all",
  priority: "all",
  risk: "all",
  scope: "all",
  dependency: "all",
};

// Keep this narrow: additional signals such as pending approvals or blocked
// dependencies must come from verified server data, not inferred from age/risk.
export function isNeedsAttention(
  task: Pick<Task, "status">,
  dependencies?: TaskDependencyView,
): boolean {
  return (
    task.status === "failed" ||
    task.status === "spec_review" ||
    dependencies?.needsIntervention === true
  );
}

export function filterTasks(
  tasks: readonly Task[],
  filters: TaskFilters,
  index: ReadonlyMap<string, TaskDependencyView> = buildTaskDependencyIndex(
    tasks,
  ),
): Task[] {
  const query = filters.query.trim().toLowerCase();
  return tasks.filter((task) => {
    const dependencies = index.get(task.id);
    if (
      filters.scope === "needs_attention" &&
      !isNeedsAttention(task, dependencies)
    ) {
      return false;
    }
    if (filters.dependency === "blocked" && !dependencies?.blocked) {
      return false;
    }
    if (
      filters.dependency === "needs_intervention" &&
      !dependencies?.needsIntervention
    ) {
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

export type DependencyState =
  | "complete"
  | "waiting"
  | "failed"
  | "unavailable"
  | "invalid";

export interface TaskDependencyItem {
  // An id is exposed only when the prerequisite is visible in this project.
  id?: string;
  title: string;
  status?: TaskStatus;
  state: DependencyState;
}

export interface TaskDependencyView {
  dependencies: TaskDependencyItem[];
  dependents: Array<Pick<Task, "id" | "title" | "status">>;
  blocked: boolean;
  needsIntervention: boolean;
}

const NOT_VISIBLE = "Not visible in this project";
const MAX_DEPENDENCIES = 100;

// The worker reads this exact metadata key. This parser intentionally does not
// treat missing metadata as evidence that execution can be admitted.
function configuredDependencies(task: Task): string[] | null {
  const scheduler = task.metadata?.scheduler;
  if (scheduler == null) return [];
  if (typeof scheduler !== "object" || Array.isArray(scheduler)) {
    return null;
  }
  const value = (scheduler as Record<string, unknown>).depends_on;
  if (value == null) return [];
  if (
    !Array.isArray(value) ||
    value.length > MAX_DEPENDENCIES ||
    value.some((id) => typeof id !== "string" || id.trim() === "")
  ) {
    return null;
  }
  return [...new Set(value as string[])];
}

/**
 * Read-only project snapshot, not a scheduler admission decision.
 * The worker remains authoritative about ownership, resource capacity,
 * budgets, approvals and task state. Cross-project references are never
 * resolved using data from another project's response.
 */
export function buildTaskDependencyIndex(
  tasks: readonly Task[],
): Map<string, TaskDependencyView> {
  const byId = new Map(tasks.map((task) => [task.id, task]));
  const views = new Map<string, TaskDependencyView>();

  for (const task of tasks) {
    const ids = configuredDependencies(task);
    const dependencies: TaskDependencyItem[] =
      ids === null
        ? [{ state: "invalid", title: "Invalid dependency configuration" }]
        : ids.map((id): TaskDependencyItem => {
            if (id === task.id) {
              return { state: "invalid", title: "Self-dependency" };
            }
            const prerequisite = byId.get(id);
            if (!prerequisite || prerequisite.project_id !== task.project_id) {
              return { state: "unavailable", title: NOT_VISIBLE };
            }
            const state: DependencyState =
              prerequisite.status === "done"
                ? "complete"
                : prerequisite.status === "failed" ||
                    prerequisite.status === "cancelled"
                  ? "failed"
                  : "waiting";
            return {
              id: prerequisite.id,
              title: prerequisite.title,
              status: prerequisite.status,
              state,
            };
          });

    views.set(task.id, {
      dependencies,
      dependents: [],
      blocked: dependencies.some(({ state }) => state !== "complete"),
      needsIntervention: dependencies.some(
        ({ state }) =>
          state === "failed" || state === "unavailable" || state === "invalid",
      ),
    });
  }

  // Detect cycles only among visible project tasks. This is a diagnostic
  // warning, not a replacement for scheduler validation or admission.
  const visited = new Set<string>();
  const active = new Map<string, number>();
  const path: string[] = [];
  const cyclic = new Set<string>();
  function visit(id: string): void {
    const start = active.get(id);
    if (start !== undefined) {
      for (const member of path.slice(start)) {
        cyclic.add(member);
      }
      return;
    }
    if (visited.has(id)) return;
    active.set(id, path.length);
    path.push(id);
    for (const dependency of views.get(id)?.dependencies ?? []) {
      if (dependency.id) visit(dependency.id);
    }
    path.pop();
    active.delete(id);
    visited.add(id);
  }
  for (const id of views.keys()) visit(id);
  for (const id of cyclic) {
    const view = views.get(id);
    if (!view) continue;
    view.dependencies = view.dependencies.map((dependency) =>
      dependency.id && cyclic.has(dependency.id)
        ? { ...dependency, state: "invalid" }
        : dependency,
    );
    view.blocked = true;
    view.needsIntervention = true;
  }

  for (const task of tasks) {
    for (const dependency of views.get(task.id)?.dependencies ?? []) {
      if (!dependency.id) continue;
      views.get(dependency.id)?.dependents.push({
        id: task.id,
        title: task.title,
        status: task.status,
      });
    }
  }
  return views;
}
