'use client';

import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useStore } from '@/lib/store';
import { useParams } from 'next/navigation';
import Link from 'next/link';
import { Card } from '@/components/ui/card';
import { Tabs } from '@/components/ui/tabs';
import { Loading } from '@/components/common/Loading';
import { StatusBadge } from '@/components/common/StatusBadge';
import { TimeAgo } from '@/components/common/TimeAgo';
import {
  FolderGit,
  GitBranch,
  ListTodo,
  Settings,
  ArrowLeft,
  ExternalLink,
} from 'lucide-react';

export default function ProjectDetailPage() {
  const params = useParams();
  const projectId = params.id as string;
  const { setSelectedProject } = useStore();

  const { data: project, isLoading: projectLoading } = useQuery({
    queryKey: ['project', projectId],
    queryFn: () => api.getProject(projectId),
  });

  const { data: repos } = useQuery({
    queryKey: ['repos', projectId],
    queryFn: () => api.listRepos(projectId),
    enabled: !!projectId,
  });

  const { data: tasks } = useQuery({
    queryKey: ['tasks', projectId],
    queryFn: () => api.listTasks(projectId),
    enabled: !!projectId,
  });

  if (projectLoading) return <Loading />;

  const repoList = repos?.data || repos || [];
  const taskList = tasks?.data || tasks || [];

  const tabs = [
    { id: 'overview', label: 'Overview', icon: FolderGit },
    { id: 'tasks', label: 'Tasks', icon: ListTodo },
    { id: 'repositories', label: 'Repositories', icon: GitBranch },
    { id: 'settings', label: 'Settings', icon: Settings },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <Link
          href="/projects"
          className="text-sm text-gray-500 hover:text-gray-300 flex items-center gap-1 mb-3"
        >
          <ArrowLeft className="w-4 h-4" />
          Back to Projects
        </Link>
        <h1 className="text-2xl font-bold text-white">{project?.name}</h1>
        {project?.description && (
          <p className="text-gray-500 mt-1">{project.description}</p>
        )}
      </div>

      {/* Stats */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <Card>
          <div className="flex items-center gap-3">
            <ListTodo className="w-5 h-5 text-blue-400" />
            <div>
              <div className="text-2xl font-bold text-white">{taskList.length}</div>
              <div className="text-xs text-gray-500">Tasks</div>
            </div>
          </div>
        </Card>
        <Card>
          <div className="flex items-center gap-3">
            <GitBranch className="w-5 h-5 text-green-400" />
            <div>
              <div className="text-2xl font-bold text-white">{repoList.length}</div>
              <div className="text-xs text-gray-500">Repositories</div>
            </div>
          </div>
        </Card>
        <Card>
          <div className="flex items-center gap-3">
            <FolderGit className="w-5 h-5 text-purple-400" />
            <div>
              <div className="text-2xl font-bold text-white">
                {taskList.filter((t: any) => t.status === 'running').length}
              </div>
              <div className="text-xs text-gray-500">Active Runs</div>
            </div>
          </div>
        </Card>
      </div>

      {/* Quick links */}
      <div className="flex gap-3">
        <Link href={`/projects/${projectId}/tasks`} className="btn-primary">
          <ListTodo className="w-4 h-4 mr-2" />
          View Task Board
        </Link>
        <Link href={`/projects/${projectId}/settings`} className="btn-secondary">
          <Settings className="w-4 h-4 mr-2" />
          Project Settings
        </Link>
      </div>

      {/* Recent Tasks */}
      <div>
        <h2 className="text-lg font-semibold text-white mb-3">Recent Tasks</h2>
        <div className="space-y-2">
          {taskList.slice(0, 5).map((task: any) => (
            <Link key={task.id} href={`/tasks/${task.id}`}>
              <Card className="hover:border-blue-500/30 transition-colors">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <StatusBadge status={task.status} />
                    <span className="text-white font-medium">{task.title}</span>
                  </div>
                  <div className="flex items-center gap-3 text-xs text-gray-500">
                    <span className="capitalize">{task.priority} priority</span>
                    <TimeAgo date={task.created_at} />
                  </div>
                </div>
              </Card>
            </Link>
          ))}
          {taskList.length === 0 && (
            <div className="text-center py-8 text-gray-500">
              No tasks yet. Create one to get started.
            </div>
          )}
        </div>
      </div>

      {/* Connected Repos */}
      <div>
        <h2 className="text-lg font-semibold text-white mb-3">Connected Repositories</h2>
        <div className="space-y-2">
          {repoList.map((repo: any) => (
            <Card key={repo.id}>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-3">
                  <GitBranch className="w-5 h-5 text-gray-400" />
                  <div>
                    <div className="text-white font-medium">{repo.full_name}</div>
                    <div className="text-xs text-gray-500">{repo.clone_url}</div>
                  </div>
                </div>
                <StatusBadge status={repo.connection_status} />
              </div>
            </Card>
          ))}
          {repoList.length === 0 && (
            <div className="text-center py-8 text-gray-500">
              No repositories connected yet.
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
