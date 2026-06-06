'use client';

import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useStore } from '@/lib/store';
import { Card } from '@/components/ui/card';
import { Badge } from '@/components/ui/badge';
import { Loading } from '@/components/common/Loading';
import { FolderGit, GitBranch, ListTodo, ArrowRight } from 'lucide-react';
import Link from 'next/link';

export default function ProjectsPage() {
  const { selectedOrg } = useStore();
  const { data: projects, isLoading } = useQuery({
    queryKey: ['projects', selectedOrg],
    queryFn: () =>
      selectedOrg ? api.listProjects(selectedOrg) : Promise.resolve([]),
    enabled: !!selectedOrg,
  });

  if (isLoading) return <Loading />;

  const projectList = projects?.data || projects || [];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">Projects</h1>
          <p className="text-gray-500 mt-1">Manage your projects and repositories</p>
        </div>
        <button
          className="btn-primary"
          onClick={() => {
            /* TODO: open create project modal */
          }}
        >
          New Project
        </button>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {projectList.map((project: any) => (
          <Link key={project.id} href={`/projects/${project.id}`}>
            <Card className="hover:border-blue-500/50 transition-colors cursor-pointer h-full">
              <div className="flex items-start gap-3">
                <div className="p-2 rounded-lg bg-blue-500/10">
                  <FolderGit className="w-5 h-5 text-blue-400" />
                </div>
                <div className="flex-1 min-w-0">
                  <h3 className="font-semibold text-white truncate">
                    {project.name}
                  </h3>
                  <p className="text-sm text-gray-500 mt-1 line-clamp-2">
                    {project.description || 'No description'}
                  </p>
                  <div className="flex items-center gap-4 mt-3 text-xs text-gray-500">
                    <span className="flex items-center gap-1">
                      <GitBranch className="w-3 h-3" />
                      {project.repo_count || 0} repos
                    </span>
                    <span className="flex items-center gap-1">
                      <ListTodo className="w-3 h-3" />
                      {project.task_count || 0} tasks
                    </span>
                  </div>
                </div>
                <ArrowRight className="w-4 h-4 text-gray-600 flex-shrink-0" />
              </div>
            </Card>
          </Link>
        ))}
      </div>

      {projectList.length === 0 && (
        <div className="text-center py-12 text-gray-500">
          <FolderGit className="w-12 h-12 mx-auto mb-3 text-gray-700" />
          <p className="text-lg font-medium">No projects yet</p>
          <p className="text-sm mt-1">Create your first project to get started</p>
        </div>
      )}
    </div>
  );
}
