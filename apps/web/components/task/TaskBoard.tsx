'use client';

import React from 'react';
import type { Task, TaskStatus } from '@/lib/types';
import { TaskCard } from './TaskCard';

const columns: { status: TaskStatus; title: string }[] = [
  { status: 'backlog', title: 'Backlog' },
  { status: 'spec_review', title: 'Spec Review' },
  { status: 'approved', title: 'Approved' },
  { status: 'running', title: 'Running' },
  { status: 'reviewing', title: 'Reviewing' },
  { status: 'pr_created', title: 'PR Created' },
  { status: 'done', title: 'Done' },
];

interface TaskBoardProps {
  tasks: Task[];
  onTaskMove?: (taskId: string, status: TaskStatus) => void;
}

export function TaskBoard({ tasks, onTaskMove }: TaskBoardProps) {
  return (
    <div className="flex gap-4 overflow-x-auto pb-4 h-full">
      {columns.map((col) => {
        const colTasks = tasks.filter((t) => t.status === col.status);
        return (
          <div key={col.status} className="flex-shrink-0 w-80 flex flex-col">
            <div className="flex items-center justify-between mb-3 px-1">
              <h3 className="font-semibold text-sm text-gray-300">{col.title}</h3>
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
