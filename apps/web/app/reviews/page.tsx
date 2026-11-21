"use client";

import { type ReviewPR, filterReviewPRs } from "@/lib/review-analytics";
import { useReviewWorkspace } from "@/lib/review-workspace";
import {
  ArrowRight,
  GitMerge,
  GitPullRequest,
  Info,
  RefreshCcw,
  Search,
  ShieldCheck,
} from "lucide-react";
import Link from "next/link";
import { useMemo, useState } from "react";

type Filter = "all" | ReviewPR["status"];
const statuses: { value: Filter; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "all", label: "All PRs" },
  { value: "merged", label: "Merged" },
  { value: "closed", label: "Closed" },
];

function StateLabel({ pr }: { pr: ReviewPR }) {
  if (pr.status === "merged")
    return (
      <span className="text-xs font-medium text-[#107c61] bg-[#e7f8f1] px-2.5 py-1 rounded-full">
        Merged
      </span>
    );
  if (pr.status === "closed")
    return (
      <span className="text-xs font-medium text-[#a14b43] bg-[#fff0ee] px-2.5 py-1 rounded-full">
        Closed
      </span>
    );
  return (
    <span className="text-xs font-medium text-[#365cb4] bg-[#eff3ff] px-2.5 py-1 rounded-full">
      {pr.draft ? "Draft" : "Open"}
    </span>
  );
}

export default function ReviewInboxPage() {
  const {
    selectedOrg,
    data,
    isPending,
    isError,
    isFetching,
    refetch,
    repoNames,
  } = useReviewWorkspace();
  const [status, setStatus] = useState<Filter>("open");
  const [repositoryId, setRepositoryId] = useState("all");
  const [search, setSearch] = useState("");
  const [limit, setLimit] = useState(40);

  const filtered = useMemo(
    () =>
      filterReviewPRs(
        data?.prs || [],
        {
          status,
          repositoryId,
          search,
        },
        repoNames,
      ),
    [data?.prs, repoNames, status, repositoryId, search],
  );
  const count = (state: Filter) =>
    state === "all"
      ? data?.prs.length || 0
      : (data?.prs || []).filter((pr) => pr.status === state).length;

  return (
    <main className="min-h-[calc(100vh-56px)] bg-[#fff] p-4 lg:p-6 text-[#1b1b1b]">
      <div className="max-w-[1370px] mx-auto space-y-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <p className="text-xs uppercase tracking-[.12em] text-[#999] font-semibold">
              Engineering review
            </p>
            <h1 className="text-xl lg:text-2xl font-semibold mt-1">
              Pull request inbox
            </h1>
            <p className="text-sm text-[#737373] mt-1">
              Triage pull requests created by your connected Dev Plane projects.
            </p>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              disabled={!selectedOrg || isFetching}
              onClick={() => void refetch()}
              className="inline-flex gap-2 items-center text-sm border border-[#e0e0e0] py-2 px-3 rounded-xl hover:bg-[#fafafa] disabled:opacity-50"
            >
              <RefreshCcw
                size={15}
                className={isFetching ? "animate-spin" : ""}
              />{" "}
              Refresh
            </button>
            <Link
              href="/reviews/analytics"
              className="inline-flex items-center gap-2 rounded-xl px-4 py-2 bg-[#171717] text-sm text-white hover:bg-[#3a3a3a]"
            >
              Analytics <ArrowRight size={15} />
            </Link>
          </div>
        </div>

        {data?.failedProjects ? (
          <div
            role="alert"
            className="bg-[#fff9ef] border border-[#f8e8c2] text-[#805a2b] text-sm p-3 rounded-xl"
          >
            {data.failedProjects} project{data.failedProjects === 1 ? "" : "s"}{" "}
            could not be fully loaded. Results may be incomplete.
          </div>
        ) : null}
        {isError ? (
          <p
            role="alert"
            className="text-sm text-red-700 p-4 border border-red-200 rounded-xl"
          >
            Unable to load review activity. Check the API connection and access.
          </p>
        ) : null}
        {!selectedOrg ? (
          <p className="text-sm p-4 border border-[#e8e8e8] rounded-xl">
            Sign in and choose an organization to load the PR inbox.
          </p>
        ) : null}

        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          {[
            {
              title: "Open PRs",
              state: "open" as Filter,
              icon: GitPullRequest,
            },
            { title: "Merged", state: "merged" as Filter, icon: GitMerge },
            { title: "Closed", state: "closed" as Filter, icon: ShieldCheck },
            { title: "Total tracked", state: "all" as Filter, icon: Info },
          ].map((metric) => (
            <button
              type="button"
              key={metric.title}
              onClick={() => setStatus(metric.state)}
              className="rounded-2xl border border-[#e6e6e6] p-4 text-left hover:border-[#c2c2c2] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[#999]"
            >
              <div className="flex justify-between text-sm text-[#707070]">
                <span>{metric.title}</span>
                <metric.icon size={17} />
              </div>
              <p className="text-2xl font-semibold tabular-nums mt-3">
                {selectedOrg && isPending ? "—" : count(metric.state)}
              </p>
            </button>
          ))}
        </div>

        <section className="border border-[#e5e5e5] rounded-2xl overflow-hidden">
          <div className="p-4 flex flex-wrap items-center justify-between gap-3 border-b border-[#eaeaea]">
            <div
              className="flex items-center gap-1 overflow-x-auto"
              aria-label="PR status"
            >
              {statuses.map((item) => (
                <button
                  key={item.value}
                  type="button"
                  aria-pressed={status === item.value}
                  onClick={() => {
                    setStatus(item.value);
                    setLimit(40);
                  }}
                  className={`px-3 py-2 text-sm whitespace-nowrap rounded-lg ${status === item.value ? "bg-[#eaeaea] font-semibold" : "text-[#777] hover:bg-[#f5f5f5]"}`}
                >
                  {item.label}{" "}
                  <span className="text-xs text-[#8a8a8a] ml-1">
                    {count(item.value)}
                  </span>
                </button>
              ))}
            </div>
            <div className="flex flex-wrap gap-2 items-center">
              <label className="relative">
                <Search
                  size={16}
                  className="absolute top-2.5 left-3 text-[#999]"
                />
                <input
                  aria-label="Search PRs"
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  placeholder="Search pull requests"
                  className="pl-9 pr-3 py-2 border border-[#e4e4e4] rounded-xl text-sm w-[210px] focus:outline-none focus:ring-2 focus:ring-[#aaa]"
                />
              </label>
              <select
                aria-label="Filter repository"
                value={repositoryId}
                onChange={(e) => setRepositoryId(e.target.value)}
                className="py-2 px-3 border border-[#e4e4e4] rounded-xl text-sm bg-white max-w-[230px]"
              >
                <option value="all">All repositories</option>
                {(data?.repos || []).map((repo) => (
                  <option key={repo.id} value={repo.id}>
                    {repo.full_name || repo.name}
                  </option>
                ))}
              </select>
            </div>
          </div>
          {selectedOrg && isPending ? (
            <p className="p-10 text-center text-[#777] text-sm">
              Loading pull requests…
            </p>
          ) : filtered.length === 0 ? (
            <div className="p-10 text-center">
              <GitPullRequest size={28} className="mx-auto text-[#ccc] mb-3" />
              <p className="font-medium">No matching pull requests</p>
              <p className="text-sm text-[#888] mt-1">
                Try a different status or repository filter.
              </p>
              {data?.projects === 0 && (
                <Link
                  href="/projects"
                  className="underline text-sm mt-3 inline-block"
                >
                  Connect a project
                </Link>
              )}
            </div>
          ) : (
            <>
              <div className="divide-y divide-[#ededed]">
                {filtered.slice(0, limit).map((pr) => (
                  <div
                    key={pr.id}
                    className="px-4 py-4 hover:bg-[#fafafa] flex items-start gap-3"
                  >
                    <GitPullRequest
                      size={19}
                      className="text-[#909090] mt-0.5 shrink-0"
                    />
                    <div className="min-w-0 flex-1">
                      <Link
                        href={`/reviews/${encodeURIComponent(pr.id)}`}
                        className="text-sm font-semibold hover:underline break-words"
                      >
                        {pr.title || `PR #${pr.number ?? pr.id}`}
                      </Link>
                      <p className="text-xs text-[#888] mt-1.5 break-words">
                        {repoNames.get(pr.repository_id) || pr.repository_id}
                        {typeof pr.number === "number"
                          ? ` · #${pr.number}`
                          : ""}
                        {" · Created "}
                        {pr.created_at.slice(0, 10)} UTC
                      </p>
                    </div>
                    <StateLabel pr={pr} />
                    <Link
                      href={`/reviews/${encodeURIComponent(pr.id)}`}
                      aria-label={`Review ${pr.title || pr.id}`}
                      className="p-1 text-[#777] hover:text-black"
                    >
                      <ArrowRight size={18} />
                    </Link>
                  </div>
                ))}
              </div>
              {filtered.length > limit && (
                <div className="p-4 border-t border-[#ededed] text-center">
                  <button
                    type="button"
                    onClick={() => setLimit(limit + 40)}
                    className="text-sm font-medium px-4 py-2 rounded-lg border border-[#e3e3e3] hover:bg-[#fafafa]"
                  >
                    Show more ({filtered.length - limit} remaining)
                  </button>
                </div>
              )}
            </>
          )}
        </section>
        <p className="text-xs text-[#999]">
          Review data is limited to PRs recorded by connected Dev Plane
          projects. Statuses represent the current locally stored state, not
          live GitHub availability.
        </p>
      </div>
    </main>
  );
}
