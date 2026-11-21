"use client";

import { api } from "@/lib/api";
import { type ReviewPR, normalizeReviewPR } from "@/lib/review-analytics";
import { useStore } from "@/lib/store";
import type { Repository } from "@/lib/types";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";

interface ProjectRef {
  id: string;
}

function rows<T>(data: unknown): T[] {
  if (Array.isArray(data)) return data as T[];
  if (data && typeof data === "object" && "data" in data) {
    const result = (data as { data: unknown }).data;
    if (Array.isArray(result)) return result as T[];
  }
  return [];
}

/** A bounded, read-only fetch across the current organization's projects.
 * Failed PR and repository requests are independent: usable rows survive
 * partial failures, which are explicitly signaled to the caller.
 */
export async function loadReviewWorkspace(orgId: string) {
  const projects = rows<ProjectRef>(await api.listProjects(orgId));
  const prs: ReviewPR[] = [];
  const repos: Repository[] = [];
  let failedProjects = 0;

  for (let offset = 0; offset < projects.length; offset += 5) {
    const batch = projects.slice(offset, offset + 5);
    const results = await Promise.all(
      batch.map(async (project) => {
        const [prResult, repoResult] = await Promise.allSettled([
          api.listPullRequests(project.id),
          api.listRepos(project.id),
        ]);
        return { prResult, repoResult };
      }),
    );
    for (const { prResult, repoResult } of results) {
      if (prResult.status === "fulfilled") {
        prs.push(
          ...rows<unknown>(prResult.value)
            .map(normalizeReviewPR)
            .filter((pr): pr is ReviewPR => pr !== null),
        );
      }
      if (repoResult.status === "fulfilled") {
        repos.push(...rows<Repository>(repoResult.value));
      }
      if (prResult.status === "rejected" || repoResult.status === "rejected") {
        failedProjects++;
      }
    }
  }

  return { projects: projects.length, failedProjects, prs, repos };
}

export function useReviewWorkspace() {
  const { selectedOrg } = useStore();
  const query = useQuery({
    queryKey: ["review-analytics", selectedOrg],
    queryFn: () => loadReviewWorkspace(selectedOrg!),
    enabled: !!selectedOrg,
    staleTime: 60_000,
  });
  const data = query.data;
  const repoNames = useMemo(
    () =>
      new Map(
        (data?.repos || []).map((repo) => [
          repo.id,
          repo.full_name || repo.name,
        ]),
      ),
    [data?.repos],
  );
  return { ...query, selectedOrg, data, repoNames };
}
