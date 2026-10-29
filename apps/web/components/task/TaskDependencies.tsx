"use client";

import { StatusBadge } from "@/components/common/StatusBadge";
import type { TaskDependencyView } from "@/lib/task-view";
import Link from "next/link";

interface TaskDependenciesProps {
  view: TaskDependencyView;
}

const stateLabels = {
  complete: "Complete",
  waiting: "Waiting",
  failed: "Needs intervention",
  unavailable: "Unverified",
  invalid: "Invalid",
} as const;

export function TaskDependencies({ view }: TaskDependenciesProps) {
  if (view.dependencies.length === 0 && view.dependents.length === 0) {
    return null;
  }

  return (
    <section aria-label="Task dependencies" className="space-y-3">
      <h2 className="text-lg font-semibold text-white">
        Dependencies and downstream tasks
      </h2>
      {view.dependencies.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium text-gray-300">
            Prerequisites ({view.dependencies.length})
          </h3>
          {view.dependencies.map((dependency, index) => (
            <div
              key={dependency.id ?? `unavailable-${index}`}
              className="flex items-center justify-between gap-3 rounded-md border border-[#30363d] bg-[#161b22] p-3"
            >
              {dependency.id ? (
                <Link
                  href={`/tasks/${dependency.id}`}
                  className="text-sm text-blue-400 hover:text-blue-300"
                >
                  {dependency.title}
                </Link>
              ) : (
                <span className="text-sm text-gray-400">
                  {dependency.title}
                </span>
              )}
              <div className="flex shrink-0 items-center gap-2">
                <span
                  className={`text-xs ${dependency.state === "complete" ? "text-green-400" : dependency.state === "waiting" ? "text-amber-300" : "text-red-300"}`}
                >
                  {stateLabels[dependency.state]}
                </span>
                {dependency.status && (
                  <StatusBadge status={dependency.status} size="sm" />
                )}
              </div>
            </div>
          ))}
        </div>
      )}
      {view.dependents.length > 0 && (
        <div className="space-y-2">
          <h3 className="text-sm font-medium text-gray-300">
            Direct downstream tasks ({view.dependents.length})
          </h3>
          {view.dependents.map((dependent) => (
            <div
              key={dependent.id}
              className="flex items-center justify-between gap-3 rounded-md border border-[#30363d] bg-[#161b22] p-3"
            >
              <Link
                href={`/tasks/${dependent.id}`}
                className="text-sm text-blue-400 hover:text-blue-300"
              >
                {dependent.title}
              </Link>
              <StatusBadge status={dependent.status} size="sm" />
            </div>
          ))}
        </div>
      )}
      <p className="text-xs text-gray-500">
        This is a read-only project snapshot. The scheduler makes the live
        admission decision and also checks resource capacity, file ownership,
        budgets, and approval policy.
      </p>
    </section>
  );
}
