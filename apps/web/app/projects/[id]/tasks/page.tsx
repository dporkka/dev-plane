'use client';

import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useParams } from 'next/navigation';
import { TaskBoard } from '@/components/task/TaskBoard';
import { CreateTaskDialog } from '@/components/task/CreateTaskDialog';
import { Loading } from '@/components/common/Loading';
import Link from 'next/link';
import { ArrowLeft, Plus } from 'lucide-react';

export default function ProjectTaskBoardPage() {
  const params = useParams();
  const projectId = params.id as string;
  const queryClient = useQueryClient();
  const [dialogOpen, setDialogOpen] = useState(false);

  const { data: tasks, isLoading: tasksLoading } = useQuery({
    queryKey: ['tasks', projectId],
    queryFn: () => api.listTasks(projectId),
    enabled: !!projectId,
  });

  const { data: repos, isLoading: reposLoading } = useQuery({
    queryKey: ['repos', projectId],
    queryFn: () => api.listRepos(projectId),
    enabled: !!projectId,
  });

  if (tasksLoading || reposLoading) return <Loading />;

  const taskList = tasks?.data || tasks || [];
  const repoList = repos?.data || repos || [];

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
              {taskList.length} tasks
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

      <div className="flex-1 min-h-0">
        <TaskBoard tasks={taskList} />
      </div>

      <CreateTaskDialog
        projectId={projectId}
        repos={repoList}
        open={dialogOpen}
        onClose={() => {
          setDialogOpen(false);
          queryClient.invalidateQueries({ queryKey: ['tasks', projectId] });
        }}
      />
    </div>
  );
}
