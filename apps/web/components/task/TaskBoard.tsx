"use client";
import { TASK_BOARD_COLUMNS } from "@/lib/task-view";
import type { Task } from "@/lib/types";
import { TaskCard } from "./TaskCard";

interface TaskBoardProps {
  tasks: Task[];
}

export function TaskBoard({ tasks }: TaskBoardProps) {
  return (
    <div className="flex gap-4 overflow-x-auto pb-4 h-full">
      {TASK_BOARD_COLUMNS.map((col) => {
        const colTasks = tasks.filter((t) => t.status === col.status);
        return (
          <div key={col.status} className="flex-shrink-0 w-80 flex flex-col">
            <div className="flex items-center justify-between mb-3 px-1">
              <h3 className="font-semibold text-sm text-gray-300">
                {col.title}
              </h3>
              <span className="text-xs text-gray-500 bg-[#21262d] px-2 py-0.5 rounded-full">
                {colTasks.length}
              </span>
            </div>
            <div className="space-y-2 flex-1 overflow-y-auto min-h-0">
              {colTasks.map((task) => (
                <TaskCard key={task.id} task={task} />
              ))}
              {colTasks.length === 0 && (
                <div className="text-center py-8 text-gray-600 text-sm border border-dashed border-[#30363d] rounded-lg">
                  No tasks
                </div>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
