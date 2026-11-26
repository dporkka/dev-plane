"use client";
import { StatusBadge } from "@/components/common/StatusBadge";
import { TimeAgo } from "@/components/common/TimeAgo";
import type { TaskDependencyView } from "@/lib/task-view";
import type { Task } from "@/lib/types";
import { cn } from "@/lib/utils";
import { AlertTriangle } from "lucide-react";
import Link from "next/link";

interface TaskCardProps {
  task: Task;
  dependencies?: TaskDependencyView;
}

export function TaskCard({ task, dependencies }: TaskCardProps) {
  const priorityColors = {
    low: "border-l-gray-600",
    medium: "border-l-yellow-600",
    high: "border-l-orange-600",
    urgent: "border-l-red-600",
  };

  return (
    <Link href={`/tasks/${task.id}`}>
      <div
        className={cn(
          "p-3 rounded-lg border border-[#30363d] bg-[#161b22] hover:border-blue-500/30 transition-all cursor-pointer",
          "border-l-[3px]",
          priorityColors[task.priority] || priorityColors.medium,
        )}
      >
        <h4 className="text-sm font-medium text-gray-200 line-clamp-2 mb-2">
          {task.title}
        </h4>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-1.5">
            <StatusBadge status={task.status} size="sm" />
          </div>
          <div className="flex items-center gap-2">
            {task.risk_level !== "low" && (
              <AlertTriangle
                className="w-3 h-3 text-red-400"
                aria-label={`${task.risk_level} risk`}
              />
            )}
            <TimeAgo date={task.created_at} className="text-xs text-gray-600" />
          </div>
        </div>
        {dependencies?.blocked && (
          <p
            className={`mt-2 text-xs ${dependencies.needsIntervention ? "text-red-300" : "text-amber-300"}`}
            aria-label="Dependency status"
          >
            {dependencies.needsIntervention
              ? "Dependency intervention needed"
              : "Waiting on prerequisites"}
            {" · "}
            {
              dependencies.dependencies.filter(
                (dependency) => dependency.state !== "complete",
              ).length
            }{" "}
            unmet
          </p>
        )}
        {(dependencies?.dependents.length ?? 0) > 0 && (
          <p className="mt-1 text-xs text-gray-400">
            Blocks {dependencies?.dependents.length} downstream task(s)
          </p>
        )}
        {task.max_cost && (
          <div className="mt-2 text-xs text-gray-600">
            Max: ${task.max_cost}
          </div>
        )}
      </div>
    </Link>
  );
}
