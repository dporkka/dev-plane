"use client";

import { api } from "@/lib/api";
import { normalizeReviewPR } from "@/lib/review-analytics";
import { useReviewWorkspace } from "@/lib/review-workspace";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowLeft,
  ArrowUpRight,
  CheckCircle2,
  Clock3,
  Code,
  FileCode2,
  GitBranch,
  Info,
  Layers,
} from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";

function GitHubLink({ url }: { url?: string }) {
  if (!url) return null;
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== "https:" || parsed.hostname !== "github.com")
      return null;
  } catch {
    return null;
  }
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className="rounded-lg bg-[#1d1d1d] text-white text-sm px-4 py-2 inline-flex items-center gap-2 hover:bg-[#393939]"
    >
      View on GitHub <ArrowUpRight size={15} />
    </a>
  );
}

function Tag({ children }: { children: React.ReactNode }) {
  return (
    <span className="inline-flex px-2.5 py-1 text-xs border border-[#e3e3e3] bg-[#fafafa] rounded-full text-[#555]">
      {children}
    </span>
  );
}

function ReportCard({
  children,
  title,
}: { title: string; children: React.ReactNode }) {
  return (
    <section className="border border-[#e7e7e7] bg-white rounded-2xl p-5">
      <h2 className="font-semibold text-sm mb-4">{title}</h2>
      {children}
    </section>
  );
}

export default function ReviewDetailsPage() {
  const params = useParams<{ id: string }>();
  const id = params.id;
  const {
    user,
    selectedOrg,
    data: workspace,
    isPending: workspacePending,
    isError: workspaceError,
    repoNames,
  } = useReviewWorkspace();
  // A direct PR URL is only viewable if it occurs in the active organization's
  // authorized PR listing. Avoid showing cached details after an org switch.
  const workspacePR = workspace?.prs.find((item) => item.id === id);
  const prQuery = useQuery({
    queryKey: ["review-pr", user?.id, selectedOrg, id],
    queryFn: () => api.getPullRequest(id),
    enabled: !!user && !!selectedOrg && !!workspacePR,
    staleTime: 60_000,
  });
  const candidate = normalizeReviewPR(prQuery.data);
  const pr =
    workspacePR && candidate?.repository_id === workspacePR.repository_id
      ? candidate
      : null;
  const reviewQuery = useQuery({
    queryKey: ["review-report", user?.id, selectedOrg, pr?.run_id],
    queryFn: () => api.getReview(pr!.run_id!),
    enabled: !!user && !!selectedOrg && !!pr?.run_id,
    retry: false,
    staleTime: 60_000,
  });
  const review = reviewQuery.data;
  const findings = review?.findings || [];
  const severityCount = (severity: string) =>
    findings.filter((item) => item.severity?.toLowerCase() === severity).length;
  const highCount = severityCount("critical") + severityCount("high");

  if (!user || !selectedOrg) {
    return (
      <main className="p-8 text-sm text-[#777]">
        Sign in and select an organization to view pull request details.
      </main>
    );
  }
  if (workspacePending || (!!workspacePR && prQuery.isPending))
    return <div className="p-8 text-sm text-[#777]">Loading pull request…</div>;
  if (workspaceError || !workspacePR || prQuery.isError || !pr)
    return (
      <main className="p-8 space-y-4">
        <Link
          href="/reviews"
          className="flex gap-2 items-center text-sm hover:underline"
        >
          <ArrowLeft size={16} /> Back to PR inbox
        </Link>
        <div
          role="alert"
          className="rounded-xl border border-[#eed0d0] text-[#993e3e] p-5"
        >
          Unable to load this pull request, or access has been denied.
        </div>
      </main>
    );

  return (
    <main className="min-h-[calc(100vh-56px)] bg-[#fff] p-4 lg:p-6 text-[#1b1b1b]">
      <div className="max-w-[1320px] mx-auto space-y-5">
        <Link
          href="/reviews"
          className="inline-flex items-center gap-2 text-sm text-[#666] hover:text-black hover:underline"
        >
          <ArrowLeft size={16} /> All pull requests
        </Link>
        <div className="flex flex-wrap justify-between items-start gap-4">
          <div className="min-w-0">
            <p className="text-xs text-[#888] mb-2">
              {repoNames.get(pr.repository_id) || "Repository"}{" "}
              {typeof pr.number === "number" ? `/ #${pr.number}` : ""}
            </p>
            <h1 className="text-xl lg:text-2xl font-semibold break-words max-w-[850px]">
              {pr.title || "Pull request"}
            </h1>
            <div className="mt-3 flex items-center flex-wrap gap-2">
              <Tag>
                {pr.status === "merged"
                  ? "Merged"
                  : pr.status === "closed"
                    ? "Closed"
                    : pr.draft
                      ? "Draft"
                      : "Open"}
              </Tag>
              <Tag>Created {pr.created_at.slice(0, 10)} UTC</Tag>
              {pr.merged_at && (
                <Tag>Merged {pr.merged_at.slice(0, 10)} UTC</Tag>
              )}
              {pr.branch && (
                <Tag>
                  {pr.branch} {pr.base_branch ? `→ ${pr.base_branch}` : ""}
                </Tag>
              )}
            </div>
          </div>
          <GitHubLink url={pr.url} />
        </div>

        <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,2fr)_minmax(290px,1fr)] gap-4">
          <div className="space-y-4 min-w-0">
            <ReportCard title="Review report">
              {!pr.run_id ? (
                <div className="flex gap-3 text-sm text-[#777]">
                  <Info size={19} className="shrink-0" />
                  <p>
                    No agent run is linked to this pull request. There is no Dev
                    Plane review report to display.
                  </p>
                </div>
              ) : reviewQuery.isPending ? (
                <p className="text-sm text-[#777]">
                  Loading the recorded review…
                </p>
              ) : reviewQuery.isError || !review ? (
                <div className="text-sm text-[#777] flex items-start gap-3">
                  <Info size={18} className="shrink-0" />
                  <div>
                    <p>
                      The review report was not found or could not be loaded.
                    </p>
                    <button
                      type="button"
                      onClick={() => void reviewQuery.refetch()}
                      className="mt-2 underline text-[#444]"
                    >
                      Retry report
                    </button>
                  </div>
                </div>
              ) : (
                <div className="space-y-5">
                  <div className="flex flex-wrap items-center gap-2">
                    <Tag>Risk: {review.risk_level || "unknown"}</Tag>
                    <Tag>
                      {review.approvable
                        ? "Review indicates approvable"
                        : "Review needs attention"}
                    </Tag>
                    <Tag>{findings.length} findings</Tag>
                  </div>
                  <div>
                    <p className="text-xs font-medium uppercase tracking-wide text-[#999] mb-2">
                      Summary
                    </p>
                    <p className="text-sm text-[#444] whitespace-pre-wrap">
                      {review.summary || "No summary supplied."}
                    </p>
                  </div>
                  {review.security_notes && (
                    <div>
                      <p className="text-xs font-medium uppercase tracking-wide text-[#999] mb-2">
                        Security notes
                      </p>
                      <p className="text-sm text-[#444] whitespace-pre-wrap">
                        {review.security_notes}
                      </p>
                    </div>
                  )}
                  {review.test_coverage && (
                    <div>
                      <p className="text-xs font-medium uppercase tracking-wide text-[#999] mb-2">
                        Test coverage
                      </p>
                      <p className="text-sm text-[#444] whitespace-pre-wrap">
                        {review.test_coverage}
                      </p>
                    </div>
                  )}
                  {review.suggestions?.length ? (
                    <div>
                      <p className="text-xs font-medium uppercase tracking-wide text-[#999] mb-2">
                        Suggestions
                      </p>
                      <ul className="list-disc pl-5 space-y-1 text-sm text-[#555]">
                        {review.suggestions.map((note, index) => (
                          <li key={index}>{note}</li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                </div>
              )}
            </ReportCard>
            <ReportCard
              title={`Findings ${review ? `(${findings.length})` : ""}`}
            >
              {!review ? (
                <p className="text-sm text-[#999]">
                  Review findings will appear when a persisted report is
                  available.
                </p>
              ) : findings.length === 0 ? (
                <p className="text-sm text-[#777] flex items-center gap-2">
                  <CheckCircle2 size={17} className="text-[#1b9b75]" /> No
                  findings recorded in this report. Review completeness still
                  requires the applicable CI and policy checks.
                </p>
              ) : (
                <div className="space-y-3">
                  {findings.map((finding, index) => (
                    <article
                      key={`${finding.file}-${finding.line}-${index}`}
                      className="border border-[#e8e8e8] rounded-xl p-4"
                    >
                      <div className="flex flex-wrap items-center gap-2 mb-2">
                        <Tag>{finding.severity || "unspecified"}</Tag>
                        <Tag>{finding.category || "general"}</Tag>
                        {finding.file && (
                          <span className="font-mono text-xs text-[#777] break-all">
                            {finding.file}
                            {finding.line ? `:${finding.line}` : ""}
                          </span>
                        )}
                      </div>
                      <p className="text-sm text-[#333] whitespace-pre-wrap">
                        {finding.message}
                      </p>
                      {finding.suggestion && (
                        <p className="text-xs text-[#686868] whitespace-pre-wrap mt-3">
                          <strong>Suggested:</strong> {finding.suggestion}
                        </p>
                      )}
                    </article>
                  ))}
                </div>
              )}
            </ReportCard>
          </div>

          <aside className="space-y-4">
            <ReportCard title="Review signals">
              {review ? (
                <div className="space-y-3 text-sm">
                  <div className="flex justify-between">
                    <span className="text-[#777]">Critical/high findings</span>
                    <strong className="tabular-nums">{highCount}</strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-[#777]">Medium findings</span>
                    <strong className="tabular-nums">
                      {severityCount("medium")}
                    </strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-[#777]">Low findings</span>
                    <strong className="tabular-nums">
                      {severityCount("low")}
                    </strong>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-[#777]">Review verdict</span>
                    <strong>
                      {review.approvable ? "Approvable" : "Needs work"}
                    </strong>
                  </div>
                  <p className="text-xs text-[#929292] border-t border-[#eee] pt-3">
                    This is the recorded agent review, not a live GitHub
                    approval or CI merge gate.
                  </p>
                </div>
              ) : (
                <p className="text-sm text-[#999]">
                  No review signals available.
                </p>
              )}
            </ReportCard>
            <ReportCard title="Linked workflow">
              <div className="space-y-3 text-sm">
                {pr.task_id ? (
                  <Link
                    href={`/tasks/${encodeURIComponent(pr.task_id)}`}
                    className="flex items-center gap-2 text-[#424242] hover:underline"
                  >
                    <Layers size={16} /> Task details <ArrowUpRight size={14} />
                  </Link>
                ) : (
                  <p className="text-[#999]">No linked task</p>
                )}
                {pr.run_id ? (
                  <Link
                    href={`/runs/${encodeURIComponent(pr.run_id)}`}
                    className="flex items-center gap-2 text-[#424242] hover:underline"
                  >
                    <Code size={16} /> Agent run <ArrowUpRight size={14} />
                  </Link>
                ) : (
                  <p className="text-[#999]">No linked run</p>
                )}
                {pr.branch ? (
                  <p className="flex items-center gap-2 text-[#777] break-all">
                    <GitBranch size={16} className="shrink-0" /> {pr.branch}
                  </p>
                ) : null}
                <p className="flex items-center gap-2 text-[#777]">
                  <Clock3 size={16} /> Last update{" "}
                  {pr.updated_at?.slice(0, 10) || "unknown"}
                </p>
              </div>
            </ReportCard>
            {review?.diff_summary && (
              <ReportCard title="Diff summary">
                <div className="space-y-2 text-sm text-[#666]">
                  <p className="flex gap-2 items-center">
                    <FileCode2 size={16} />{" "}
                    {review.diff_summary.files_changed ?? "—"} files changed
                  </p>
                  <p>
                    +{review.diff_summary.insertions ?? 0} / −
                    {review.diff_summary.deletions ?? 0} lines
                  </p>
                </div>
              </ReportCard>
            )}
            <p className="text-xs text-[#929292]">
              Read-only review. Merge decisions remain governed by the
              repository's required checks and authorization policy.
            </p>
          </aside>
        </div>
      </div>
    </main>
  );
}
