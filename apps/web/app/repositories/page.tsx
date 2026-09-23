"use client";

import { Loading } from "@/components/common/Loading";
import { StatusBadge } from "@/components/common/StatusBadge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ExternalLink,
  GitBranch,
  Plus,
  RefreshCw,
  Search,
  Trash2,
} from "lucide-react";
import { type FormEvent, useState } from "react";

type ForgeProvider = "github" | "gitea";
type VCSBackend = "git" | "jj";

function parseRepositoryInput(
  value: string,
  provider: ForgeProvider,
  configuredBaseUrl: string,
  vcsBackend: VCSBackend,
) {
  const raw = value.trim().replace(/\.git$/i, "");
  let owner = "";
  let name = "";
  let baseUrl =
    provider === "github"
      ? "https://github.com"
      : configuredBaseUrl.trim().replace(/\/$/, "");

  if (/^https?:\/\//i.test(raw)) {
    const parsed = new URL(raw);
    const parts = parsed.pathname.split("/").filter(Boolean);
    if (parts.length < 2) {
      throw new Error("Repository URL must include owner/repository.");
    }
    name = parts.pop() || "";
    owner = parts.pop() || "";
    if (provider === "gitea") {
      baseUrl = parsed.origin + (parts.length ? `/${parts.join("/")}` : "");
    } else if (parsed.hostname.toLowerCase() !== "github.com") {
      throw new Error("Select Gitea for non-github.com repository URLs.");
    }
  } else {
    const normalized = raw
      .replace(/^git@github\.com:/i, "")
      .replace(/^\/+|\/+$/g, "");
    const parts = normalized.split("/");
    if (parts.length !== 2) {
      throw new Error("Enter a repository as owner/repo or an HTTPS URL.");
    }
    [owner, name] = parts;
  }

  if (!owner || !name) {
    throw new Error("Repository owner and name are required.");
  }
  if (provider === "gitea" && !baseUrl) {
    throw new Error("Gitea base URL is required for owner/repo input.");
  }

  return {
    owner,
    name,
    provider,
    base_url: baseUrl,
    vcs_backend: vcsBackend,
  };
}

export default function RepositoriesPage() {
  const { selectedProject } = useStore();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [showConnect, setShowConnect] = useState(false);
  const [newRepoUrl, setNewRepoUrl] = useState("");
  const [forgeProvider, setForgeProvider] = useState<ForgeProvider>("github");
  const [forgeBaseUrl, setForgeBaseUrl] = useState("https://gitea.com");
  const [vcsBackend, setVcsBackend] = useState<VCSBackend>("git");
  const [actionError, setActionError] = useState<string | null>(null);

  const { data: repos, isLoading } = useQuery({
    queryKey: ["repos", selectedProject],
    queryFn: () =>
      selectedProject ? api.listRepos(selectedProject) : Promise.resolve([]),
    enabled: !!selectedProject,
  });
  const invalidateRepos = () =>
    queryClient.invalidateQueries({ queryKey: ["repos", selectedProject] });
  const connectRepo = useMutation({
    mutationFn: () => {
      if (!selectedProject) {
        throw new Error("Select a project before connecting a repository.");
      }
      return api.connectRepo(
        selectedProject,
        parseRepositoryInput(
          newRepoUrl,
          forgeProvider,
          forgeBaseUrl,
          vcsBackend,
        ),
      );
    },
    onSuccess: async () => {
      await invalidateRepos();
      setNewRepoUrl("");
      setShowConnect(false);
      setActionError(null);
    },
    onError: (error) => {
      setActionError(
        error instanceof Error
          ? error.message
          : "Failed to connect repository.",
      );
    },
  });
  const syncRepo = useMutation({
    mutationFn: (repoId: string) => api.syncRepo(repoId),
    onSuccess: async () => {
      await invalidateRepos();
      setActionError(null);
    },
    onError: (error) => {
      setActionError(
        error instanceof Error ? error.message : "Failed to sync repository.",
      );
    },
  });
  const disconnectRepo = useMutation({
    mutationFn: (repoId: string) => api.disconnectRepo(repoId),
    onSuccess: async () => {
      await invalidateRepos();
      setActionError(null);
    },
    onError: (error) => {
      setActionError(
        error instanceof Error
          ? error.message
          : "Failed to disconnect repository.",
      );
    },
  });

  if (isLoading) return <Loading />;

  const repoList = repos?.data || repos || [];
  const filtered = repoList.filter((r: any) =>
    r.full_name?.toLowerCase().includes(search.toLowerCase()),
  );
  const submitRepository = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!newRepoUrl.trim()) {
      setActionError("Repository is required.");
      return;
    }
    setActionError(null);
    connectRepo.mutate();
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold text-white">Repositories</h1>
          <p className="text-gray-500 mt-1">Connected GitHub and Gitea repositories</p>
        </div>
        <button
          onClick={() => setShowConnect(!showConnect)}
          className="btn-primary flex items-center gap-2"
          disabled={!selectedProject}
        >
          <Plus className="w-4 h-4" />
          Connect Repo
        </button>
      </div>

      {/* Connect new repo form */}
      {showConnect && (
        <Card>
          <form onSubmit={submitRepository} className="space-y-3">
            <div className="grid gap-3 md:grid-cols-2">
              <label className="space-y-1 text-sm font-medium text-gray-300">
                <span>Git forge</span>
                <select
                  value={forgeProvider}
                  onChange={(event) =>
                    setForgeProvider(event.target.value as ForgeProvider)
                  }
                  className="h-10 w-full rounded-md border border-[#30363d] bg-[#0d1117] px-3 text-sm text-gray-200"
                  disabled={connectRepo.isPending}
                >
                  <option value="github">GitHub</option>
                  <option value="gitea">Gitea</option>
                </select>
              </label>
              <label className="space-y-1 text-sm font-medium text-gray-300">
                <span>Agent VCS</span>
                <select
                  value={vcsBackend}
                  onChange={(event) =>
                    setVcsBackend(event.target.value as VCSBackend)
                  }
                  className="h-10 w-full rounded-md border border-[#30363d] bg-[#0d1117] px-3 text-sm text-gray-200"
                  disabled={connectRepo.isPending}
                >
                  <option value="git">Git</option>
                  <option value="jj">Jujutsu (jj)</option>
                </select>
              </label>
            </div>
            {forgeProvider === "gitea" && (
              <label className="block space-y-1 text-sm font-medium text-gray-300">
                <span>Gitea base URL</span>
                <Input
                  value={forgeBaseUrl}
                  onChange={(event) => setForgeBaseUrl(event.target.value)}
                  placeholder="https://git.example.com"
                  disabled={connectRepo.isPending}
                />
              </label>
            )}
            <label
              htmlFor="repository-url"
              className="block text-sm font-medium text-gray-300"
            >
              Repository URL or full name (owner/repo)
            </label>
            <div className="flex flex-col gap-2 sm:flex-row">
              <Input
                id="repository-url"
                value={newRepoUrl}
                onChange={(e) => setNewRepoUrl(e.target.value)}
                placeholder={
                  forgeProvider === "github"
                    ? "myorg/myrepo or https://github.com/myorg/myrepo"
                    : "myorg/myrepo or https://git.example.com/myorg/myrepo"
                }
                className="flex-1"
                disabled={connectRepo.isPending}
              />
              <Button
                type="submit"
                disabled={connectRepo.isPending || !selectedProject}
                className="shrink-0"
              >
                {connectRepo.isPending ? "Connecting..." : "Connect"}
              </Button>
            </div>
          </form>
        </Card>
      )}

      {actionError && (
        <div className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
          {actionError}
        </div>
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
                    <span>Branch: {repo.default_branch || "main"}</span>
                    <span className="uppercase">{repo.forge_provider || "github"}</span>
                    <span>{repo.vcs_backend === "jj" ? "jj" : "git"}</span>
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
                  onClick={() => syncRepo.mutate(repo.id)}
                  className="p-2 rounded-md hover:bg-gray-800 text-gray-400 hover:text-gray-200 transition-colors"
                  title="Sync repository"
                  disabled={syncRepo.isPending || disconnectRepo.isPending}
                >
                  <RefreshCw className="w-4 h-4" />
                </button>
                <a
                  href={`${repo.forge_base_url || "https://github.com"}/${repo.full_name}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="p-2 rounded-md hover:bg-gray-800 text-gray-400 hover:text-gray-200 transition-colors"
                  title={`Open in ${repo.forge_provider === "gitea" ? "Gitea" : "GitHub"}`}
                >
                  <ExternalLink className="w-4 h-4" />
                </a>
                <button
                  onClick={() => {
                    if (confirm("Disconnect this repository?")) {
                      disconnectRepo.mutate(repo.id);
                    }
                  }}
                  className="p-2 rounded-md hover:bg-red-500/10 text-gray-400 hover:text-red-400 transition-colors"
                  title="Disconnect"
                  disabled={syncRepo.isPending || disconnectRepo.isPending}
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
            Connect a GitHub or Gitea repository to start creating tasks
          </p>
        </div>
      )}
    </div>
  );
}
