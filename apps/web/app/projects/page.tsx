'use client';

import { type FormEvent, useMemo, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useStore } from '@/lib/store';
import { Card } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Dialog } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Loading } from '@/components/common/Loading';
import { FolderGit, GitBranch, ListTodo, ArrowRight, Plus } from 'lucide-react';
import Link from 'next/link';

function slugify(value: string) {
  return value
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 80);
}

export default function ProjectsPage() {
  const { selectedOrg } = useStore();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [showCreateProject, setShowCreateProject] = useState(false);
  const [projectName, setProjectName] = useState('');
  const [projectSlug, setProjectSlug] = useState('');
  const [projectDescription, setProjectDescription] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const { data: projects, isLoading } = useQuery({
    queryKey: ['projects', selectedOrg],
    queryFn: () =>
      selectedOrg ? api.listProjects(selectedOrg) : Promise.resolve([]),
    enabled: !!selectedOrg,
  });
  const effectiveSlug = useMemo(
    () => projectSlug.trim() || slugify(projectName),
    [projectName, projectSlug]
  );
  const createProject = useMutation({
    mutationFn: () => {
      if (!selectedOrg) {
        throw new Error('Select an organization before creating a project.');
      }
      return api.createProject(selectedOrg, {
        name: projectName.trim(),
        slug: effectiveSlug,
        description: projectDescription.trim(),
      });
    },
    onSuccess: async (project: any) => {
      await queryClient.invalidateQueries({ queryKey: ['projects', selectedOrg] });
      setShowCreateProject(false);
      setProjectName('');
      setProjectSlug('');
      setProjectDescription('');
      setFormError(null);
      if (project?.id) {
        router.push(`/projects/${project.id}`);
      }
    },
    onError: (error) => {
      setFormError(error instanceof Error ? error.message : 'Failed to create project.');
    },
  });

  if (isLoading) return <Loading />;

  const projectList = projects?.data || projects || [];

  const submitProject = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!projectName.trim()) {
      setFormError('Project name is required.');
      return;
    }
    if (!effectiveSlug) {
      setFormError('Project slug is required.');
      return;
    }
    setFormError(null);
    createProject.mutate();
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">Projects</h1>
          <p className="text-gray-500 mt-1">Manage your projects and repositories</p>
        </div>
        <button
          className="btn-primary flex items-center gap-2"
          onClick={() => setShowCreateProject(true)}
          disabled={!selectedOrg}
        >
          <Plus className="w-4 h-4" />
          New Project
        </button>
      </div>

      <Dialog
        open={showCreateProject}
        onClose={() => {
          if (!createProject.isPending) {
            setShowCreateProject(false);
            setFormError(null);
          }
        }}
        title="New Project"
      >
        <form onSubmit={submitProject} className="space-y-4">
          <div className="space-y-2">
            <label htmlFor="project-name" className="block text-sm font-medium text-gray-300">
              Name
            </label>
            <Input
              id="project-name"
              value={projectName}
              onChange={(event) => setProjectName(event.target.value)}
              placeholder="Mobile checkout"
              autoFocus
              disabled={createProject.isPending}
            />
          </div>
          <div className="space-y-2">
            <label htmlFor="project-slug" className="block text-sm font-medium text-gray-300">
              Slug
            </label>
            <Input
              id="project-slug"
              value={projectSlug}
              onChange={(event) => setProjectSlug(slugify(event.target.value))}
              placeholder={slugify(projectName) || 'mobile-checkout'}
              disabled={createProject.isPending}
            />
          </div>
          <div className="space-y-2">
            <label htmlFor="project-description" className="block text-sm font-medium text-gray-300">
              Description
            </label>
            <Textarea
              id="project-description"
              value={projectDescription}
              onChange={(event) => setProjectDescription(event.target.value)}
              placeholder="Scope, ownership, or repository notes"
              disabled={createProject.isPending}
            />
          </div>
          {formError && (
            <div className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
              {formError}
            </div>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <Button
              type="button"
              variant="secondary"
              onClick={() => {
                setShowCreateProject(false);
                setFormError(null);
              }}
              disabled={createProject.isPending}
            >
              Cancel
            </Button>
            <Button type="submit" disabled={createProject.isPending || !selectedOrg}>
              {createProject.isPending ? 'Creating...' : 'Create Project'}
            </Button>
          </div>
        </form>
      </Dialog>

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
