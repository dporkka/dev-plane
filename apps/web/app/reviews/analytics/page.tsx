"use client";

import { api } from "@/lib/api";
import {
  type ReviewPR,
  csvCell,
  medianHoursToMerge,
  normalizeReviewPR,
  summarizePullRequests,
} from "@/lib/review-analytics";
import { useStore } from "@/lib/store";
import type { Repository } from "@/lib/types";
import { useQuery } from "@tanstack/react-query";
import {
  CalendarDays,
  ChevronRight,
  Download,
  ExternalLink,
  GitMerge,
  GitPullRequest,
  Info,
  LockKeyhole,
  RefreshCw,
  Search,
  ShieldCheck,
  SlidersHorizontal,
} from "lucide-react";
import Link from "next/link";
import { useEffect, useMemo, useState } from "react";

type ProjectRef = { id: string; name?: string };
type Section = "summary" | "quality" | "time" | "security" | "details";
const sections: { id: Section; label: string }[] = [
  { id: "summary", label: "Summary" },
  { id: "quality", label: "Quality metrics" },
  { id: "time", label: "Time metrics" },
  { id: "security", label: "PR security reviews" },
  { id: "details", label: "Review details" },
];

function rows<T>(response: unknown): T[] {
  if (Array.isArray(response)) return response as T[];
  if (response && typeof response === "object" && "data" in response) {
    const result = (response as { data: unknown }).data;
    if (Array.isArray(result)) return result as T[];
  }
  return [];
}

function dateString(date: Date) {
  return [
    date.getFullYear(),
    String(date.getMonth() + 1).padStart(2, "0"),
    String(date.getDate()).padStart(2, "0"),
  ].join("-");
}

function defaultStart() {
  const date = new Date();
  date.setDate(date.getDate() - 29);
  return dateString(date);
}

function saveCsv(records: ReviewPR[], repoNames: Map<string, string>) {
  const quote = csvCell;
  const lines = [
    ["Repository", "PR", "Title", "Status", "Created (UTC)", "URL"]
      .map(quote)
      .join(","),
    ...records.map((pr) =>
      [
        repoNames.get(pr.repository_id) || pr.repository_id,
        pr.number || "",
        pr.title || "",
        pr.status,
        pr.created_at,
        pr.url || "",
      ]
        .map(quote)
        .join(","),
    ),
  ];
  const blob = new Blob([lines.join("\r\n")], {
    type: "text/csv;charset=utf-8",
  });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = "dev-plane-pr-activity.csv";
  anchor.click();
  URL.revokeObjectURL(url);
}

function MetricCard({
  title,
  children,
  note,
  className = "",
}: {
  title: string;
  children: React.ReactNode;
  note?: string;
  className?: string;
}) {
  return (
    <article
      className={`min-h-[146px] rounded-[18px] border border-[#e6e6e6] bg-white p-[18px] ${className}`}
    >
      <div className="flex items-start justify-between gap-3">
        <h3 className="text-sm font-semibold text-[#1a1a1a]">{title}</h3>
        {note && (
          <span
            title={note}
            aria-label={note}
            className="cursor-help text-[#747474]"
          >
            <Info size={15} />
          </span>
        )}
      </div>
      {children}
    </article>
  );
}

function NotTracked({ detail }: { detail: string }) {
  return (
    <div className="flex min-h-[90px] items-center justify-center flex-col text-center gap-1">
      <p className="text-xl font-semibold text-[#404040]">Not yet tracked</p>
      <p className="max-w-[300px] text-xs text-[#858585]">{detail}</p>
    </div>
  );
}

export default function ReviewAnalyticsPage() {
  const { selectedOrg } = useStore();
  const [section, setSection] = useState<Section>("summary");
  // A static Next.js page must not bake the build day's date into hydrated inputs.
  const [start, setStart] = useState("");
  const [end, setEnd] = useState("");
  useEffect(() => {
    setStart(defaultStart());
    setEnd(dateString(new Date()));
  }, []);
  const [repositoryId, setRepositoryId] = useState("all");
  // A repository ID is meaningful only within its organization.
  useEffect(() => {
    setRepositoryId("all");
  }, [selectedOrg]);
  const [filtersVisible, setFiltersVisible] = useState(false);
  const [search, setSearch] = useState("");

  const { data, isPending, isFetching, isError, refetch } = useQuery({
    queryKey: ["review-analytics", selectedOrg],
    enabled: !!selectedOrg,
    staleTime: 60_000,
    queryFn: async () => {
      const projects = rows<ProjectRef>(await api.listProjects(selectedOrg!));
      const settled = await Promise.allSettled(
        projects.map(async (project) => {
          const [prs, repos] = await Promise.all([
            api.listPullRequests(project.id),
            api.listRepos(project.id),
          ]);
          return {
            prs: rows<unknown>(prs)
              .map(normalizeReviewPR)
              .filter((pr): pr is ReviewPR => pr !== null),
            repos: rows<Repository>(repos),
          };
        }),
      );
      const complete = settled.filter((item) => item.status === "fulfilled");
      return {
        projects: projects.length,
        failedProjects: settled.length - complete.length,
        prs: complete.flatMap((item) => item.value.prs),
        repos: complete.flatMap((item) => item.value.repos),
      };
    },
  });

  const prs = data?.prs || [];
  const repos = data?.repos || [];
  const repoNames = useMemo(
    () => new Map(repos.map((repo) => [repo.id, repo.full_name || repo.name])),
    [repos],
  );
  const summary = useMemo(
    () => summarizePullRequests(prs, start, end, repositoryId),
    [prs, start, end, repositoryId],
  );
  const repoActivity = useMemo(() => {
    const counts = new Map<string, number>();
    for (const pr of summary.prs) {
      counts.set(pr.repository_id, (counts.get(pr.repository_id) || 0) + 1);
    }
    return [...counts].sort((a, b) => b[1] - a[1]).slice(0, 5);
  }, [summary.prs]);
  const activeRepos = new Set(summary.prs.map((pr) => pr.repository_id)).size;
  const mergeRate =
    summary.total > 0 ? Math.round((summary.merged / summary.total) * 100) : 0;
  const medianMergeHours = useMemo(
    () => medianHoursToMerge(summary.prs),
    [summary.prs],
  );
  const sortedPrs = useMemo(
    () =>
      [...summary.prs].sort((a, b) => b.created_at.localeCompare(a.created_at)),
    [summary.prs],
  );
  const searchedPrs = sortedPrs.filter((pr) =>
    `${pr.title || ""} ${repoNames.get(pr.repository_id) || ""} ${pr.number || ""}`
      .toLowerCase()
      .includes(search.toLowerCase()),
  );
  const sectionsHeading: Record<Section, string> = {
    summary: "Summary",
    quality: "Quality metrics",
    time: "Time metrics",
    security: "PR security reviews",
    details: "Review details",
  };

  return (
    <div className="flex min-h-[calc(100vh-56px)] bg-white">
      <aside
        aria-label="Analytics sections"
        className="hidden md:flex w-[218px] xl:w-[246px] shrink-0 flex-col border-r border-[#e8e8e8] p-4"
      >
        <div className="space-y-1">
          {sections.map((item) => (
            <button
              type="button"
              key={item.id}
              onClick={() => setSection(item.id)}
              aria-current={section === item.id ? "page" : undefined}
              className={`w-full text-left text-sm rounded-lg px-3 py-2.5 transition-colors ${section === item.id ? "bg-[#f0f0f0] text-[#202020] font-medium" : "text-[#626262] hover:bg-[#f5f5f5]"}`}
            >
              {item.label}
            </button>
          ))}
        </div>
        <div className="mt-auto border-t border-[#eee] pt-4">
          <Link
            href="/dashboard"
            className="flex items-center justify-between text-xs text-[#747474] hover:text-black"
          >
            Go to control plane <ChevronRight size={14} />
          </Link>
        </div>
      </aside>

      <main className="min-w-0 flex-1 p-4 lg:p-5 xl:p-6">
        <div className="flex flex-wrap items-start gap-3 justify-between mb-4">
          <div>
            <h1 className="font-semibold text-lg tracking-tight text-[#1b1b1b]">
              {sectionsHeading[section]}
            </h1>
            <p className="mt-1 text-sm text-[#6b6b6b]">
              Pull-request activity across connected Dev Plane projects and
              repositories.
            </p>
          </div>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => void refetch()}
              aria-label="Refresh analytics"
              disabled={!selectedOrg || isFetching}
              className="h-9 px-3 rounded-lg border border-[#e3e3e3] text-sm text-[#444] hover:bg-[#f5f5f5] disabled:opacity-50 flex items-center gap-2"
            >
              <RefreshCw
                size={15}
                className={isFetching ? "animate-spin" : ""}
              />{" "}
              Refresh
            </button>
            <button
              type="button"
              onClick={() => saveCsv(summary.prs, repoNames)}
              disabled={!selectedOrg || isPending}
              className="h-9 px-3 rounded-lg bg-[#1b1b1b] text-sm text-white hover:bg-[#3a3a3a] disabled:opacity-50 flex items-center gap-2"
            >
              <Download size={15} /> Export data
            </button>
          </div>
        </div>

        <div className="md:hidden flex gap-2 overflow-x-auto pb-3">
          {sections.map((item) => (
            <button
              type="button"
              key={item.id}
              onClick={() => setSection(item.id)}
              className={`shrink-0 rounded-lg px-3 py-2 text-xs ${section === item.id ? "bg-[#eaeaea] font-semibold" : "bg-[#f7f7f7] text-[#777]"}`}
            >
              {item.label}
            </button>
          ))}
        </div>

        <div className="flex flex-wrap gap-2 items-center justify-between mb-4">
          <div className="flex flex-wrap items-center gap-2">
            <span className="flex items-center gap-2 border border-[#e5e5e5] rounded-xl px-3 py-2 text-xs text-[#474747]">
              <CalendarDays size={16} />
              <label className="sr-only" htmlFor="review-start">
                From date
              </label>
              <input
                id="review-start"
                type="date"
                value={start}
                max={end}
                onChange={(e) => setStart(e.target.value)}
                className="bg-transparent max-w-[130px] outline-none"
              />
              <span className="text-[#9d9d9d]">—</span>
              <label className="sr-only" htmlFor="review-end">
                To date
              </label>
              <input
                id="review-end"
                type="date"
                value={end}
                min={start}
                onChange={(e) => setEnd(e.target.value)}
                className="bg-transparent max-w-[130px] outline-none"
              />
            </span>
            <select
              aria-label="Filter repository"
              value={repositoryId}
              onChange={(event) => setRepositoryId(event.target.value)}
              className="border border-[#e5e5e5] rounded-xl px-3 py-2 text-xs text-[#444] bg-white max-w-[250px]"
            >
              <option value="all">All repositories</option>
              {repos.map((repo) => (
                <option key={repo.id} value={repo.id}>
                  {repo.full_name || repo.name}
                </option>
              ))}
            </select>
          </div>
          <button
            type="button"
            onClick={() => setFiltersVisible(!filtersVisible)}
            aria-expanded={filtersVisible}
            className="flex items-center gap-2 border border-[#e5e5e5] rounded-xl px-3 py-2 text-sm text-[#444] hover:bg-[#fafafa]"
          >
            <SlidersHorizontal size={16} /> Filters
          </button>
        </div>
        {filtersVisible && (
          <div className="mb-4 p-3 rounded-xl bg-[#f7f7f7] text-xs text-[#616161] flex flex-wrap items-center gap-3">
            <span>
              Date ranges use PR creation timestamps (UTC), not merge completion
              times.
            </span>
            <button
              type="button"
              onClick={() => {
                setStart(defaultStart());
                setEnd(dateString(new Date()));
                setRepositoryId("all");
              }}
              className="ml-auto text-[#222] underline underline-offset-2"
            >
              Reset filters
            </button>
          </div>
        )}

        {!selectedOrg && (
          <div className="rounded-xl border border-[#e4e4e4] p-5 text-sm text-[#666]">
            Select an organization from the top right to view your review
            activity.
          </div>
        )}
        {isError && (
          <div
            role="alert"
            className="rounded-xl border border-[#eed0d0] p-5 mb-4 text-sm text-[#9c3737]"
          >
            Review activity could not be loaded. Check your connection and
            access to the Dev Plane API.
          </div>
        )}
        {data && data.failedProjects > 0 && (
          <div
            aria-live="polite"
            className="mb-4 rounded-xl bg-[#fff8ed] p-3 text-sm text-[#855d2a]"
          >
            Data is incomplete: {data.failedProjects} of {data.projects}{" "}
            projects could not be loaded. Totals cover only accessible projects.
          </div>
        )}

        {section === "summary" && (
          <div className="space-y-4">
            <div className="grid gap-4 lg:grid-cols-3">
              <MetricCard
                title="Active Repositories"
                note="Distinct repositories with PRs created in the selected date window."
              >
                <p className="mt-5 text-center text-[27px] leading-none font-semibold tabular-nums">
                  {isPending && selectedOrg ? "—" : activeRepos}
                </p>
                <p className="text-center text-xs text-[#909090] mt-2">
                  with PR activity
                </p>
              </MetricCard>
              <MetricCard
                title="Merged Pull Requests"
                note="PRs created in the selected dates whose current state is merged. This is not a count of merges occurring during the period."
              >
                <div className="mt-4 flex justify-around text-center">
                  <div>
                    <p className="text-xs text-[#777]">Currently merged</p>
                    <p className="font-semibold text-[25px] mt-1 tabular-nums">
                      {isPending && selectedOrg ? "—" : summary.merged}
                    </p>
                  </div>
                  <div>
                    <p className="text-xs text-[#777]">Of created PRs</p>
                    <p className="font-semibold text-[25px] mt-1 tabular-nums">
                      {isPending && selectedOrg ? "—" : summary.total}
                    </p>
                  </div>
                </div>
              </MetricCard>
              <MetricCard
                title="Open Pull Requests"
                note="PRs created in the reporting window and currently open."
              >
                <div className="mt-4 flex justify-around text-center">
                  <div>
                    <p className="text-xs text-[#777]">Open</p>
                    <p className="font-semibold text-[25px] mt-1 tabular-nums">
                      {isPending && selectedOrg ? "—" : summary.open}
                    </p>
                  </div>
                  <div>
                    <p className="text-xs text-[#777]">Closed</p>
                    <p className="font-semibold text-[25px] mt-1 tabular-nums">
                      {isPending && selectedOrg ? "—" : summary.closed}
                    </p>
                  </div>
                </div>
              </MetricCard>
            </div>

            <div className="grid gap-4 lg:grid-cols-3">
              <MetricCard
                title="Pull Request Activity"
                note="PRs created during the selected range."
              >
                <div className="mt-5 flex justify-around text-center">
                  <div>
                    <p className="text-xs text-[#777]">PRs created</p>
                    <p className="font-semibold text-2xl tabular-nums mt-1">
                      {summary.total}
                    </p>
                  </div>
                  <div>
                    <p className="text-xs text-[#777]">Tracked projects</p>
                    <p className="font-semibold text-2xl tabular-nums mt-1">
                      {data?.projects ?? "—"}
                    </p>
                  </div>
                </div>
              </MetricCard>
              <MetricCard
                title="Median Time to Merge"
                note="Median hours from PR creation to merge, among PRs created in the selected dates with recorded merge timestamps."
              >
                <div className="mt-5 flex justify-around text-center">
                  <div>
                    <p className="text-xs text-[#777]">Time to merge</p>
                    <p className="font-semibold text-2xl tabular-nums mt-1">
                      {medianMergeHours === null
                        ? "—"
                        : `${medianMergeHours} h`}
                    </p>
                  </div>
                  <div>
                    <p className="text-xs text-[#777]">Merge ratio</p>
                    <p className="font-semibold text-2xl tabular-nums mt-1">
                      {summary.total ? `${mergeRate}%` : "—"}
                    </p>
                  </div>
                </div>
              </MetricCard>
              <MetricCard
                title="Reviewer Time Saved"
                note="Requires instrumented human review baselines and measured assisted review times."
              >
                <NotTracked detail="Review duration instrumentation is not available in the current API." />
              </MetricCard>
            </div>

            <div className="grid gap-4 lg:grid-cols-3">
              <MetricCard
                title="Pull Requests by Status"
                note="Current status of PRs created in the reporting period."
                className="min-h-[300px]"
              >
                <div className="flex items-center justify-center py-6">
                  <div
                    role="img"
                    aria-label={`Merged ${summary.merged}, open ${summary.open}, closed ${summary.closed}`}
                    className="w-[178px] h-[178px] rounded-full flex items-center justify-center"
                    style={{
                      background: summary.total
                        ? `conic-gradient(#24b88f 0 ${(summary.merged / summary.total) * 100}%, #4d7eeb ${(summary.merged / summary.total) * 100}% ${((summary.merged + summary.open) / summary.total) * 100}%, #f7b848 ${((summary.merged + summary.open) / summary.total) * 100}% 100%)`
                        : "#efefef",
                    }}
                  >
                    <div className="w-[125px] h-[125px] rounded-full bg-white flex items-center justify-center flex-col">
                      <span className="text-2xl font-semibold tabular-nums">
                        {summary.total}
                      </span>
                      <span className="text-xs text-[#888]">PRs created</span>
                    </div>
                  </div>
                </div>
                <div className="flex justify-center flex-wrap gap-3 text-xs text-[#686868]">
                  <span>
                    <i className="inline-block w-2.5 h-2.5 bg-[#24b88f] mr-1 rounded-sm" />
                    Merged
                  </span>
                  <span>
                    <i className="inline-block w-2.5 h-2.5 bg-[#4d7eeb] mr-1 rounded-sm" />
                    Open
                  </span>
                  <span>
                    <i className="inline-block w-2.5 h-2.5 bg-[#f7b848] mr-1 rounded-sm" />
                    Closed
                  </span>
                </div>
              </MetricCard>
              <MetricCard
                title="Review Comments by Severity"
                note="Cannot infer comment severity from pull-request statuses."
                className="min-h-[300px]"
              >
                <div className="flex h-[245px] flex-col items-center justify-center text-center">
                  <ShieldCheck size={30} className="text-[#b5b5b5] mb-3" />
                  <NotTracked detail="Connect review findings and severity events to enable this report." />
                </div>
              </MetricCard>
              <MetricCard
                title="Repository Activity"
                note="Top repositories by PRs created during the selected dates."
                className="min-h-[300px]"
              >
                {repoActivity.length ? (
                  <div className="mt-6 space-y-4">
                    {repoActivity.map(([id, count]) => (
                      <div key={id}>
                        <div className="flex justify-between gap-2 mb-1 text-xs">
                          <span
                            className="truncate text-[#575757]"
                            title={repoNames.get(id) || id}
                          >
                            {repoNames.get(id) || id}
                          </span>
                          <span className="font-medium tabular-nums">
                            {count}
                          </span>
                        </div>
                        <div className="bg-[#f1f1f1] h-2 rounded-full overflow-hidden">
                          <div
                            className="h-full rounded-full bg-[#5b80ef]"
                            style={{
                              width: `${(count / repoActivity[0][1]) * 100}%`,
                            }}
                          />
                        </div>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="h-[240px] flex items-center justify-center text-[#9a9a9a] text-sm">
                    No PR activity in the selected range.
                  </div>
                )}
              </MetricCard>
            </div>
          </div>
        )}

        {section === "quality" && (
          <div className="grid gap-4 lg:grid-cols-2">
            <MetricCard title="PR outcomes">
              <div className="mt-8 flex justify-around text-center">
                <div>
                  <p className="text-xs text-[#777]">Merged</p>
                  <p className="text-3xl font-semibold">{summary.merged}</p>
                </div>
                <div>
                  <p className="text-xs text-[#777]">Open</p>
                  <p className="text-3xl font-semibold">{summary.open}</p>
                </div>
                <div>
                  <p className="text-xs text-[#777]">Closed</p>
                  <p className="text-3xl font-semibold">{summary.closed}</p>
                </div>
              </div>
            </MetricCard>
            <MetricCard title="Findings accepted">
              <NotTracked detail="Review finding dispositions and comment acceptance events are not aggregated by the API." />
            </MetricCard>
          </div>
        )}

        {section === "time" && (
          <div className="grid gap-4 lg:grid-cols-2">
            <MetricCard
              title="Median time to merge"
              note="Median hours between PR creation and merge for PRs created during the selected dates."
            >
              <div className="mt-6 text-center">
                <p className="font-semibold text-3xl">
                  {medianMergeHours === null
                    ? "No data"
                    : `${medianMergeHours} hours`}
                </p>
                <p className="mt-2 text-xs text-[#888]">
                  Computed from available merge timestamps
                </p>
              </div>
            </MetricCard>
            <MetricCard title="Review time saved">
              <NotTracked detail="Requires measured human baselines and reviewer interaction durations." />
            </MetricCard>
          </div>
        )}

        {section === "security" && (
          <div className="max-w-3xl rounded-[18px] border border-[#e5e5e5] bg-white p-8 text-center">
            <LockKeyhole size={30} className="mx-auto mb-4 text-[#8f8f8f]" />
            <h2 className="font-semibold">
              Security review analytics are not yet available
            </h2>
            <p className="text-sm text-[#777] mt-2">
              A reliable severity report needs persisted findings, scan
              provenance, and their dispositions. PR status alone cannot
              establish whether a security issue was found or resolved.
            </p>
            <Link
              href="/settings/policies"
              className="inline-flex items-center mt-5 text-sm font-medium underline underline-offset-4"
            >
              Review security policies{" "}
              <ExternalLink size={13} className="ml-1" />
            </Link>
          </div>
        )}

        {section === "details" && (
          <div className="rounded-[18px] border border-[#e5e5e5] overflow-hidden">
            <div className="p-4 border-b border-[#ededed] flex items-center justify-between gap-3">
              <h2 className="font-semibold text-sm">
                Pull requests{" "}
                <span className="text-[#777] font-normal">
                  ({searchedPrs.length})
                </span>
              </h2>
              <label className="relative">
                <Search
                  size={15}
                  className="absolute left-3 top-2.5 text-[#888]"
                />
                <input
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder="Search pull requests"
                  aria-label="Search pull requests"
                  className="pl-9 pr-3 py-2 rounded-lg border border-[#ddd] text-xs w-[220px] max-w-full"
                />
              </label>
            </div>
            {searchedPrs.length ? (
              <div className="divide-y divide-[#ededed]">
                {searchedPrs.slice(0, 100).map((pr) => (
                  <div key={pr.id} className="p-4 flex items-center gap-3">
                    {pr.status === "merged" ? (
                      <GitMerge size={18} className="text-[#2bb28f] shrink-0" />
                    ) : (
                      <GitPullRequest
                        size={18}
                        className="text-[#527ded] shrink-0"
                      />
                    )}
                    <div className="min-w-0 flex-1">
                      {pr.url ? (
                        <a
                          href={pr.url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="font-medium text-sm hover:underline truncate block"
                        >
                          {pr.title || `PR #${pr.number || pr.id}`}
                        </a>
                      ) : (
                        <span className="font-medium text-sm truncate block">
                          {pr.title || `PR #${pr.number || pr.id}`}
                        </span>
                      )}
                      <p className="text-xs text-[#808080] mt-1">
                        {repoNames.get(pr.repository_id) || pr.repository_id} ·{" "}
                        {pr.created_at?.slice(0, 10)}
                      </p>
                    </div>
                    <span className="text-xs capitalize text-[#666]">
                      {pr.status}
                    </span>
                  </div>
                ))}
                {searchedPrs.length > 100 && (
                  <p className="text-sm text-[#888] p-4">
                    Showing first 100. Export to view the entire loaded set.
                  </p>
                )}
              </div>
            ) : (
              <div className="p-10 text-center text-sm text-[#888]">
                No matching pull requests in this date range.
              </div>
            )}
          </div>
        )}

        {data && data.projects === 0 && (
          <div className="mt-5 text-sm text-[#888] flex items-center gap-2">
            <Info size={16} /> No connected projects found.{" "}
            <Link href="/projects" className="underline">
              Connect a project
            </Link>{" "}
            to populate reports.
          </div>
        )}
        <p className="mt-5 text-xs text-[#989898]">
          Data source: Dev Plane connected projects. Reporting counts use PR
          creation dates in UTC and current PR statuses; incomplete or
          unavailable metrics are not estimated.
        </p>
      </main>
    </div>
  );
}
