"use client";

import { Loading } from "@/components/common/Loading";
import { CreateTaskDialog } from "@/components/task/CreateTaskDialog";
import { TaskBoard } from "@/components/task/TaskBoard";
import { TaskCard } from "@/components/task/TaskCard";
import { api } from "@/lib/api";
import {
  DEFAULT_TASK_FILTERS,
  TASK_BOARD_COLUMNS,
  type TaskFilters,
  filterTasks,
  isNeedsAttention,
} from "@/lib/task-view";
import type { Task } from "@/lib/types";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Plus } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";

export default function ProjectTaskBoardPage() {
  const params = useParams();
  const projectId = params.id as string;
  const queryClient = useQueryClient();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [view, setView] = useState<"board" | "list">("board");
  const [filters, setFilters] = useState<TaskFilters>(DEFAULT_TASK_FILTERS);

  const { data: tasks, isLoading: tasksLoading } = useQuery({
    queryKey: ["tasks", projectId],
    queryFn: () => api.listTasks(projectId),
    enabled: !!projectId,
  });

  const { data: repos, isLoading: reposLoading } = useQuery({
    queryKey: ["repos", projectId],
    queryFn: () => api.listRepos(projectId),
    enabled: !!projectId,
  });

  if (tasksLoading || reposLoading) return <Loading />;

  const taskList: Task[] = tasks?.data || tasks || [];
  const repoList = repos?.data || repos || [];
  const visibleTasks = filterTasks(taskList, filters);
  const needsAttentionCount = taskList.filter(isNeedsAttention).length;

  return (
    <div className="space-y-4 h-full flex flex-col">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Link
            href={`/projects/${projectId}`}
            className="text-gray-500 hover:text-gray-300 transition-colors"
          >
            <ArrowLeft className="w-5 h-5" />
          </Link>
          <div>
            <h1 className="text-2xl font-bold text-white">Task Board</h1>
            <p className="text-gray-500 text-sm mt-0.5">
              {visibleTasks.length} of {taskList.length} tasks
            </p>
          </div>
        </div>
        <button
          onClick={() => setDialogOpen(true)}
          className="btn-primary flex items-center gap-2"
        >
          <Plus className="w-4 h-4" />
          New Task
        </button>
      </div>

      <div className="flex flex-col gap-3" aria-label="Task filters">
        <div className="flex flex-wrap items-center gap-2">
          <input
            aria-label="Search tasks"
            placeholder="Search tasks"
            type="search"
            value={filters.query}
            onChange={(event) =>
              setFilters((current) => ({
                ...current,
                query: event.target.value,
              }))
            }
            className="min-w-48 flex-1 rounded-md border border-[#30363d] bg-[#161b22] px-3 py-2 text-sm text-gray-200"
          />
          <button
            type="button"
            aria-pressed={filters.scope === "needs_attention"}
            onClick={() =>
              setFilters((current) => ({
                ...current,
                scope:
                  current.scope === "needs_attention"
                    ? "all"
                    : "needs_attention",
              }))
            }
            className={`rounded-md border px-3 py-2 text-sm ${filters.scope === "needs_attention" ? "border-red-400 text-red-300" : "border-[#30363d] text-gray-300"}`}
          >
            Needs Attention ({needsAttentionCount})
          </button>
          <div
            className="inline-flex rounded-md border border-[#30363d]"
            aria-label="Task layout"
          >
            <button
              type="button"
              aria-pressed={view === "board"}
              onClick={() => setView("board")}
              className={`px-3 py-2 text-sm ${view === "board" ? "bg-[#30363d] text-white" : "text-gray-400"}`}
            >
              Board
            </button>
            <button
              type="button"
              aria-pressed={view === "list"}
              onClick={() => setView("list")}
              className={`px-3 py-2 text-sm ${view === "list" ? "bg-[#30363d] text-white" : "text-gray-400"}`}
            >
              List
            </button>
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          <select
            aria-label="Task status"
            value={filters.status}
            onChange={(event) =>
              setFilters((current) => ({
                ...current,
                status: event.target.value as TaskFilters["status"],
              }))
            }
            className="rounded-md border border-[#30363d] bg-[#161b22] px-3 py-2 text-sm text-gray-200"
          >
            <option value="all">All statuses</option>
            {TASK_BOARD_COLUMNS.map((column) => (
              <option key={column.status} value={column.status}>
                {column.title}
              </option>
            ))}
          </select>
          <select
            aria-label="Repository"
            value={filters.repositoryId}
            onChange={(event) =>
              setFilters((current) => ({
                ...current,
                repositoryId: event.target.value,
              }))
            }
            className="rounded-md border border-[#30363d] bg-[#161b22] px-3 py-2 text-sm text-gray-200"
          >
            <option value="all">All repositories</option>
            {repoList.map((repo: { id: string; full_name: string }) => (
              <option key={repo.id} value={repo.id}>
                {repo.full_name}
              </option>
            ))}
          </select>
          <select
            aria-label="Priority"
            value={filters.priority}
            onChange={(event) =>
              setFilters((current) => ({
                ...current,
                priority: event.target.value as TaskFilters["priority"],
              }))
            }
            className="rounded-md border border-[#30363d] bg-[#161b22] px-3 py-2 text-sm text-gray-200"
          >
            <option value="all">All priorities</option>
            {(["low", "medium", "high", "urgent"] as const).map((priority) => (
              <option key={priority} value={priority}>
                {priority}
              </option>
            ))}
          </select>
          <select
            aria-label="Risk"
            value={filters.risk}
            onChange={(event) =>
              setFilters((current) => ({
                ...current,
                risk: event.target.value as TaskFilters["risk"],
              }))
            }
            className="rounded-md border border-[#30363d] bg-[#161b22] px-3 py-2 text-sm text-gray-200"
          >
            <option value="all">All risk levels</option>
            {(["low", "medium", "high", "critical"] as const).map((risk) => (
              <option key={risk} value={risk}>
                {risk}
              </option>
            ))}
          </select>
        </div>
        <p className="text-xs text-gray-500">
          Needs Attention includes failed tasks and specs awaiting review.
        </p>
      </div>

      <div className="flex-1 min-h-0 overflow-auto">
        {visibleTasks.length === 0 ? (
          <div className="rounded-md border border-dashed border-[#30363d] p-8 text-center text-gray-400">
            No tasks match the current filters.
          </div>
        ) : view === "board" ? (
          <TaskBoard tasks={visibleTasks} />
        ) : (
          <div className="grid grid-cols-1 gap-2 lg:grid-cols-2">
            {visibleTasks.map((task) => (
              <TaskCard key={task.id} task={task} />
            ))}
          </div>
        )}
      </div>

      <CreateTaskDialog
        projectId={projectId}
        repos={repoList}
        open={dialogOpen}
        onClose={() => {
          setDialogOpen(false);
          queryClient.invalidateQueries({ queryKey: ["tasks", projectId] });
        }}
      />
    </div>
  );
}
