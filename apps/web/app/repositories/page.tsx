'use client';

import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { useStore } from '@/lib/store';
import { Card } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Loading } from '@/components/common/Loading';
import { StatusBadge } from '@/components/common/StatusBadge';
import {
  GitBranch,
  Plus,
  Search,
  RefreshCw,
  ExternalLink,
  Trash2,
} from 'lucide-react';

export default function RepositoriesPage() {
  const { selectedProject } = useStore();
  const [search, setSearch] = useState('');
  const [showConnect, setShowConnect] = useState(false);
  const [newRepoUrl, setNewRepoUrl] = useState('');

  const { data: repos, isLoading, refetch } = useQuery({
    queryKey: ['repos', selectedProject],
    queryFn: () =>
      selectedProject
        ? api.listRepos(selectedProject)
        : Promise.resolve([]),
    enabled: !!selectedProject,
  });

  if (isLoading) return <Loading />;

  const repoList = repos?.data || repos || [];
  const filtered = repoList.filter((r: any) =>
    r.full_name?.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">Repositories</h1>
          <p className="text-gray-500 mt-1">
            Connected GitHub repositories
          </p>
        </div>
        <button
          onClick={() => setShowConnect(!showConnect)}
          className="btn-primary flex items-center gap-2"
        >
          <Plus className="w-4 h-4" />
          Connect Repo
        </button>
      </div>

      {/* Connect new repo form */}
      {showConnect && (
        <Card>
          <div className="space-y-3">
            <label className="block text-sm font-medium text-gray-300">
              Repository URL or full name (owner/repo)
            </label>
            <div className="flex gap-2">
              <Input
                value={newRepoUrl}
                onChange={(e) => setNewRepoUrl(e.target.value)}
                placeholder="e.g. myorg/myrepo or https://github.com/myorg/myrepo"
                className="flex-1"
              />
              <button
                onClick={() => {
                  if (selectedProject && newRepoUrl) {
                    api.connectRepo(selectedProject, {
                      full_name: newRepoUrl.replace(/^https:\/\/github\.com\//, ''),
                    });
                  }
                }}
                className="btn-primary"
              >
                Connect
              </button>
            </div>
          </div>
        </Card>
      )}

      {/* Search */}
      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-500" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search repositories..."
          className="pl-10"
        />
      </div>

      {/* Repo list */}
      <div className="space-y-2">
        {filtered.map((repo: any) => (
          <Card key={repo.id}>
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <GitBranch className="w-5 h-5 text-gray-400" />
                <div>
                  <div className="text-white font-medium">{repo.full_name}</div>
                  <div className="text-xs text-gray-500 flex items-center gap-2">
                    <span>Branch: {repo.default_branch || 'main'}</span>
                    {repo.private && (
                      <span className="bg-gray-800 px-1.5 py-0.5 rounded text-[10px]">
                        Private
                      </span>
                    )}
                  </div>
                </div>
              </div>
              <div className="flex items-center gap-2">
                <StatusBadge status={repo.connection_status} />
                <button
                  onClick={() => api.syncRepo(repo.id)}
                  className="p-2 rounded-md hover:bg-gray-800 text-gray-400 hover:text-gray-200 transition-colors"
                  title="Sync repository"
                >
                  <RefreshCw className="w-4 h-4" />
                </button>
                <a
                  href={`https://github.com/${repo.full_name}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="p-2 rounded-md hover:bg-gray-800 text-gray-400 hover:text-gray-200 transition-colors"
                  title="Open in GitHub"
                >
                  <ExternalLink className="w-4 h-4" />
                </a>
                <button
                  onClick={() => {
                    if (confirm('Disconnect this repository?')) {
                      api.disconnectRepo(repo.id);
                    }
                  }}
                  className="p-2 rounded-md hover:bg-red-500/10 text-gray-400 hover:text-red-400 transition-colors"
                  title="Disconnect"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>
          </Card>
        ))}
      </div>

      {filtered.length === 0 && !isLoading && (
        <div className="text-center py-12 text-gray-500">
          <GitBranch className="w-12 h-12 mx-auto mb-3 text-gray-700" />
          <p className="text-lg font-medium">No repositories connected</p>
          <p className="text-sm mt-1">
            Connect a GitHub repository to start creating tasks
          </p>
        </div>
      )}
    </div>
  );
}
