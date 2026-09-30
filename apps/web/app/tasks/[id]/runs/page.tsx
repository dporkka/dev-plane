"use client";

import { Loading } from "@/components/common/Loading";
import { RunTimeline } from "@/components/run/RunTimeline";
import { TaskExecutionGraph } from "@/components/task/TaskExecutionGraph";
import { api } from "@/lib/api";
import type { AgentRun } from "@/lib/types";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeft, Terminal } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";

function runListHasActiveRuns(runs: AgentRun[]): boolean {
  return runs.some((run) =>
    ["pending", "queued", "running", "paused"].includes(run.status),
  );
}

export default function RunTimelinePage() {
  const params = useParams();
  const taskId = params.id as string;

  const { data: task, isLoading: taskLoading } = useQuery({
    queryKey: ["task", taskId],
    queryFn: () => api.getTask(taskId),
  });

  const { data: runs, isLoading: runsLoading } = useQuery({
    queryKey: ["runs", taskId],
    queryFn: () => api.listRuns(taskId),
    enabled: !!taskId,
  });

  const { data: executionEvidence } = useQuery({
    queryKey: ["execution-evidence", taskId],
    queryFn: () => api.getTaskExecutionEvidence(taskId),
    enabled: !!taskId,
    refetchInterval: runListHasActiveRuns(runs?.data || runs || []) ? 5000 : false,
  });

  if (taskLoading || runsLoading) return <Loading />;

  const runList: AgentRun[] = runs?.data || runs || [];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <Link
          href={`/tasks/${taskId}`}
          className="text-sm text-gray-500 hover:text-gray-300 flex items-center gap-1 mb-3"
        >
          <ArrowLeft className="w-4 h-4" />
          Back to Task
        </Link>
        <div className="flex items-center gap-3">
          <Terminal className="w-6 h-6 text-blue-400" />
          <div>
            <h1 className="text-2xl font-bold text-white">
              Execution Timeline
            </h1>
            <p className="text-gray-500 text-sm mt-0.5">{task?.title}</p>
          </div>
        </div>
      </div>

      {runList.length > 0 && (
        <>
          <section>
            <div className="mb-3">
              <h2 className="text-lg font-semibold text-white">Run Lineage</h2>
              <p className="mt-0.5 text-xs text-gray-500">
                Parent/child execution structure across retries, handoffs, and review repairs.
              </p>
            </div>
            <TaskExecutionGraph runs={runList} evidence={executionEvidence?.runs || {}} />
          </section>

          <section className="space-y-4">
            <div>
              <h2 className="text-lg font-semibold text-white">Run Timelines</h2>
              <p className="mt-0.5 text-xs text-gray-500">
                Step-level history for each execution attempt.
              </p>
            </div>
            {runList.map((run) => (
              <RunTimeline key={run.id} run={run} />
            ))}
          </section>
        </>
      )}

      {runList.length === 0 && (
        <div className="text-center py-12 text-gray-500">
          <Terminal className="w-12 h-12 mx-auto mb-3 text-gray-700" />
          <p className="text-lg font-medium">No runs yet</p>
          <p className="text-sm mt-1">
            Start the task to see the execution timeline
          </p>
        </div>
      )}
    </div>
  );
}
