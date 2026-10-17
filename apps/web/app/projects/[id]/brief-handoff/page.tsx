"use client";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useToast } from "@/components/ui/toast";
import { api } from "@/lib/api";
import {
  buildBriefHandoffPayload,
  type BriefHandoffForm,
} from "@/lib/brief-handoff";
import type { Repository } from "@/lib/types";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, BookOpen } from "lucide-react";
import Link from "next/link";
import { useParams, useRouter } from "next/navigation";
import { type FormEvent, useState } from "react";

const initialForm: BriefHandoffForm = {
  repositoryId: "",
  title: "",
  briefProjectId: "",
  description: "",
  briefUrl: "",
  briefContent: "",
  acceptanceCriteria: "",
};

export default function BuildFromBriefPage() {
  const projectId = useParams().id as string;
  const router = useRouter();
  const queryClient = useQueryClient();
  const { addToast } = useToast();
  const [form, setForm] = useState<BriefHandoffForm>(initialForm);
  const [sourceMode, setSourceMode] = useState<"paste" | "url">("paste");
  const [formError, setFormError] = useState<string | null>(null);

  const { data: repositories, isLoading, isError } = useQuery({
    queryKey: ["repos", projectId],
    queryFn: () => api.listRepos(projectId),
    enabled: !!projectId,
  });

  const repoList: Repository[] = repositories?.data || repositories || [];

  const createHandoff = useMutation({
    mutationFn: () =>
      api.createBriefHandoff(projectId, buildBriefHandoffPayload(form)),
    onSuccess: async (task) => {
      await queryClient.invalidateQueries({ queryKey: ["tasks", projectId] });
      addToast("Brief task created for review", "success");
      router.push(`/tasks/${task.id}`);
    },
    onError: (error) => {
      setFormError(
        error instanceof Error ? error.message : "Could not create brief task.",
      );
      addToast("Could not create brief task", "error");
    },
  });

  function setField<K extends keyof BriefHandoffForm>(
    key: K,
    value: BriefHandoffForm[K],
  ) {
    setForm((previous) => ({ ...previous, [key]: value }));
    setFormError(null);
  }

  function switchSource(mode: "paste" | "url") {
    setSourceMode(mode);
    setForm((previous) => ({
      ...previous,
      briefContent: "",
      briefUrl: "",
    }));
    setFormError(null);
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    try {
      buildBriefHandoffPayload(form);
    } catch (error) {
      setFormError(error instanceof Error ? error.message : "Invalid brief.");
      return;
    }
    setFormError(null);
    createHandoff.mutate();
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6">
      <div>
        <Link
          href={`/projects/${projectId}/tasks`}
          className="mb-3 flex items-center gap-2 text-sm text-gray-400 hover:text-white"
        >
          <ArrowLeft className="h-4 w-4" />
          Task Board
        </Link>
        <div className="flex items-center gap-3">
          <BookOpen className="h-7 w-7 text-blue-400" aria-hidden="true" />
          <div>
            <h1 className="text-2xl font-bold text-white">Build from Brief</h1>
            <p className="mt-1 text-sm text-gray-400">
              Import a reviewed product brief as a Dev Plane engineering task.
            </p>
          </div>
        </div>
      </div>

      <form
        onSubmit={handleSubmit}
        className="space-y-5 rounded-xl border border-gray-800 bg-gray-900/40 p-5"
      >
        <div className="space-y-2">
          <label htmlFor="brief-repo" className="block text-sm font-medium text-gray-300">
            Target repository
          </label>
          <Select
            id="brief-repo"
            value={form.repositoryId}
            onChange={(event) => setField("repositoryId", event.target.value)}
            disabled={isLoading || isError || createHandoff.isPending}
            options={[
              {
                value: "",
                label: isLoading
                  ? "Loading repositories..."
                  : repoList.length === 0
                    ? "No connected repositories"
                    : "Select a repository",
              },
              ...repoList.map((repo) => ({
                value: repo.id,
                label: repo.full_name,
              })),
            ]}
          />
          {isError && (
            <p role="alert" className="text-sm text-red-300">
              Failed to load connected repositories.
            </p>
          )}
        </div>

        <div className="space-y-2">
          <label htmlFor="brief-title" className="block text-sm font-medium text-gray-300">
            Task title
          </label>
          <Input
            id="brief-title"
            value={form.title}
            onChange={(event) => setField("title", event.target.value)}
            placeholder="Build customer portal MVP"
            disabled={createHandoff.isPending}
            maxLength={160}
          />
        </div>

        <fieldset className="space-y-3">
          <legend className="mb-2 text-sm font-medium text-gray-300">Brief source</legend>
          <div className="flex flex-wrap gap-4 text-sm text-gray-300">
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="brief-source"
                checked={sourceMode === "paste"}
                onChange={() => switchSource("paste")}
                disabled={createHandoff.isPending}
              />
              Paste Markdown (private)
            </label>
            <label className="flex items-center gap-2">
              <input
                type="radio"
                name="brief-source"
                checked={sourceMode === "url"}
                onChange={() => switchSource("url")}
                disabled={createHandoff.isPending}
              />
              Link a public brief
            </label>
          </div>
          {sourceMode === "paste" ? (
            <div className="space-y-2">
              <label htmlFor="brief-content" className="block text-sm text-gray-300">
                Builder's Brief content
              </label>
              <Textarea
                id="brief-content"
                value={form.briefContent}
                onChange={(event) => setField("briefContent", event.target.value)}
                placeholder="# Product requirements\n\nDescribe the users, scope, screens, and technical requirements..."
                rows={12}
                maxLength={80000}
                disabled={createHandoff.isPending}
              />
              <p className="text-xs text-gray-500">
                Pasted content is stored in the authenticated task. You do not need
                to make a private Dev Plan brief public.
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              <label htmlFor="brief-url" className="block text-sm text-gray-300">
                Public HTTPS brief URL
              </label>
              <Input
                id="brief-url"
                type="url"
                value={form.briefUrl}
                onChange={(event) => setField("briefUrl", event.target.value)}
                placeholder="https://build.davidporkka.com/public/briefs/..."
                disabled={createHandoff.isPending}
              />
              <p className="text-xs text-amber-300">
                The agent must be able to access this URL without signing in. Do
                not include a private access token or other credentials.
              </p>
            </div>
          )}
        </fieldset>

        <div className="space-y-2">
          <label
            htmlFor="brief-acceptance"
            className="block text-sm font-medium text-gray-300"
          >
            Acceptance criteria (one per line)
          </label>
          <Textarea
            id="brief-acceptance"
            value={form.acceptanceCriteria}
            onChange={(event) => setField("acceptanceCriteria", event.target.value)}
            placeholder={"A customer can sign up and sign in.\nA user cannot read another tenant's records.\nThe main user journey passes browser tests."}
            rows={4}
            disabled={createHandoff.isPending}
          />
          <p className="text-xs text-gray-500">
            These are goals for review and verification, not claims that tests
            have already passed.
          </p>
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2">
            <label htmlFor="brief-project-id" className="block text-sm text-gray-300">
              Dev Plan project ID (optional)
            </label>
            <Input
              id="brief-project-id"
              value={form.briefProjectId}
              onChange={(event) => setField("briefProjectId", event.target.value)}
              disabled={createHandoff.isPending}
            />
          </div>
          <div className="space-y-2">
            <label htmlFor="brief-description" className="block text-sm text-gray-300">
              Additional instructions (optional)
            </label>
            <Input
              id="brief-description"
              value={form.description}
              onChange={(event) => setField("description", event.target.value)}
              placeholder="Keep the existing tech stack"
              disabled={createHandoff.isPending}
            />
          </div>
        </div>

        {formError && (
          <p role="alert" className="rounded-md border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-300">
            {formError}
          </p>
        )}

        <div className="flex items-center justify-between gap-3 border-t border-gray-800 pt-4">
          <p className="max-w-md text-xs text-gray-500">
            Creates a task for specification review. It does not automatically
            approve execution, merge code, or deploy.
          </p>
          <Button
            type="submit"
            disabled={createHandoff.isPending || isLoading || isError || repoList.length === 0}
          >
            {createHandoff.isPending ? "Creating task..." : "Create Brief Task"}
          </Button>
        </div>
      </form>
    </div>
  );
}
