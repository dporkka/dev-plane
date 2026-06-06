'use client';

import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useParams } from 'next/navigation';
import { RunTimeline } from '@/components/run/RunTimeline';
import { Loading } from '@/components/common/Loading';
import Link from 'next/link';
import { ArrowLeft, Terminal } from 'lucide-react';

export default function RunTimelinePage() {
  const params = useParams();
  const taskId = params.id as string;

  const { data: task, isLoading: taskLoading } = useQuery({
    queryKey: ['task', taskId],
    queryFn: () => api.getTask(taskId),
  });

  const { data: runs, isLoading: runsLoading } = useQuery({
    queryKey: ['runs', taskId],
    queryFn: () => api.listRuns(taskId),
    enabled: !!taskId,
  });

  if (taskLoading || runsLoading) return <Loading />;

  const runList = runs?.data || runs || [];

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
            <h1 className="text-2xl font-bold text-white">Execution Timeline</h1>
            <p className="text-gray-500 text-sm mt-0.5">{task?.title}</p>
          </div>
        </div>
      </div>

      {/* Run selector */}
      {runList.length > 0 && (
        <div className="space-y-4">
          {runList.map((run: any) => (
            <RunTimeline key={run.id} run={run} />
          ))}
        </div>
      )}

      {runList.length === 0 && (
        <div className="text-center py-12 text-gray-500">
          <Terminal className="w-12 h-12 mx-auto mb-3 text-gray-700" />
          <p className="text-lg font-medium">No runs yet</p>
          <p className="text-sm mt-1">Start the task to see the execution timeline</p>
        </div>
      )}
    </div>
  );
}
