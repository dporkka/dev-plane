"use client";

import { api } from "@/lib/api";
import { useStore } from "@/lib/store";
import type { ExternalPRTriageItem } from "@ai-cp/dev-plane-sdk";
import { useQuery } from "@tanstack/react-query";
import {
	AlertTriangle,
	ChevronLeft,
	ChevronRight,
	ExternalLink,
	GitPullRequest,
	RefreshCw,
} from "lucide-react";
import { type FormEvent, useState } from "react";

const PAGE_SIZE = 50;
const filters = [
	{ value: "", label: "All decisions" },
	{ value: "candidate", label: "Review candidates" },
	{ value: "review", label: "Needs review" },
	{ value: "needs-repair", label: "Needs repair" },
	{ value: "ci-blocked", label: "CI blocked" },
	{ value: "dependency-blocked", label: "Dependency blocked" },
	{ value: "draft", label: "Drafts" },
];

function snapshotAge(date: string): string {
	const ms = Date.parse(date);
	if (!Number.isFinite(ms)) return "Unknown snapshot age";
	return `${new Date(ms).toISOString().slice(0, 16).replace("T", " ")} UTC`;
}

function TriageRow({ item }: { item: ExternalPRTriageItem }) {
	const outdated =
		!Number.isFinite(Date.parse(item.snapshot_at)) ||
		Date.now() - Date.parse(item.snapshot_at) > 24 * 60 * 60 * 1000;
	return (
		<li className="rounded-xl border border-[#e6e6e6] bg-white p-4 shadow-sm">
			<div className="flex flex-wrap items-start justify-between gap-3">
				<div className="min-w-0 flex-1">
					<div className="mb-1 flex flex-wrap items-center gap-2 text-xs text-[#727272]">
						<span>
							{item.repo} #{item.number}
						</span>
						<span className="rounded-md bg-[#f0f3f8] px-2 py-0.5 font-medium text-[#315489]">
							{item.bucket}
						</span>
						{outdated && (
							<span className="rounded-md bg-[#fff4dd] px-2 py-0.5 font-medium text-[#966100]">
								Stale evidence
							</span>
						)}
					</div>
					<a
						href={item.url}
						target="_blank"
						rel="noopener noreferrer"
						className="inline-flex items-center gap-1 text-sm font-semibold text-[#315cac] hover:underline"
					>
						{item.title}{" "}
						<ExternalLink aria-hidden="true" className="h-3.5 w-3.5" />
					</a>
					<div className="mt-1 text-xs text-[#727272]">
						Score {item.score} · Snapshot {snapshotAge(item.snapshot_at)} ·{" "}
						{item.head_sha.slice(0, 8)}
					</div>
					{item.reasons.length > 0 && (
						<ul className="mt-2 list-inside list-disc text-xs text-[#454545]">
							{Array.from(new Set(item.reasons))
								.slice(0, 3)
								.map((reason) => (
									<li key={reason}>{reason}</li>
								))}
							{item.reasons.length > 3 && (
								<li>+{item.reasons.length - 3} additional reasons</li>
							)}
						</ul>
					)}
					{item.dependencies.length > 0 && (
						<p className="mt-2 text-xs text-[#885d0d]">
							Dependencies: {item.dependencies.join(", ")}
						</p>
					)}
				</div>
				<span className="text-xs text-[#727272]">
					{item.draft ? "Draft" : "Advisory"}
				</span>
			</div>
		</li>
	);
}

export default function ExternalPRTriagePage() {
	const { selectedOrg } = useStore();
	// Remount the scoped inbox when changing organizations, including pagination.
	return (
		<ExternalPRTriageWorkspace
			key={selectedOrg ?? "none"}
			selectedOrg={selectedOrg}
		/>
	);
}

function ExternalPRTriageWorkspace({
	selectedOrg,
}: { selectedOrg: string | null }) {
	const [bucket, setBucket] = useState("");
	const [repoText, setRepoText] = useState("");
	const [repo, setRepo] = useState("");
	const [page, setPage] = useState(0);

	const { data, isLoading, isFetching, error, refetch } = useQuery({
		queryKey: ["external-pr-triage", selectedOrg, bucket, repo, page],
		enabled: !!selectedOrg,
		queryFn: () =>
			selectedOrg
				? api.listExternalPRTriage(selectedOrg, {
						bucket,
						repo,
						limit: PAGE_SIZE,
						offset: page * PAGE_SIZE,
					})
				: Promise.reject(new Error("Organization is required")),
	});

	const onSearch = (event: FormEvent) => {
		event.preventDefault();
		setPage(0);
		setRepo(repoText.trim());
	};

	return (
		<section className="mx-auto max-w-5xl space-y-5 bg-white px-4 py-6 text-[#1b1b1b] lg:px-6">
			<div className="flex flex-wrap items-center justify-between gap-3">
				<div>
					<h1 className="flex items-center gap-2 text-2xl font-semibold text-[#1b1b1b]">
						<GitPullRequest aria-hidden="true" className="h-6 w-6" /> PR triage
					</h1>
					<p className="mt-1 text-sm text-[#727272]">
						Imported PR Triage snapshots across connected GitHub repositories.
						These are separate from Dev Plane-generated PRs.
					</p>
				</div>
				<button
					type="button"
					onClick={() => void refetch()}
					disabled={!selectedOrg || isFetching}
					className="inline-flex items-center gap-2 rounded-lg border border-[#e4e4e4] px-3 py-2 text-sm text-[#454545] hover:bg-[#f5f5f5] disabled:opacity-40"
				>
					<RefreshCw aria-hidden="true" className="h-4 w-4" /> Refresh cache
				</button>
			</div>

			<div
				role="note"
				className="flex gap-3 rounded-lg border border-[#f2dbac] bg-[#fffaf0] p-3 text-sm text-[#73501b]"
			>
				<AlertTriangle aria-hidden="true" className="mt-0.5 h-4 w-4 shrink-0" />
				<span>
					Classification is advisory, not merge authorization. This view shows
					imported snapshots, not live GitHub or verified CI state. Refreshing
					the page does not rescan GitHub.
				</span>
			</div>

			<div className="flex flex-wrap gap-3">
				<label className="flex flex-col gap-1 text-xs text-[#727272]">
					Decision
					<select
						value={bucket}
						onChange={(e) => {
							setBucket(e.target.value);
							setPage(0);
						}}
						className="rounded-md border border-[#dddddd] bg-white px-3 py-2 text-sm text-[#252525]"
					>
						{filters.map((f) => (
							<option key={f.value} value={f.value}>
								{f.label}
							</option>
						))}
					</select>
				</label>
				<form
					onSubmit={onSearch}
					className="flex flex-1 flex-wrap items-end gap-2"
				>
					<label className="flex min-w-44 flex-1 flex-col gap-1 text-xs text-[#727272]">
						Repository
						<input
							value={repoText}
							onChange={(e) => setRepoText(e.target.value)}
							placeholder="owner/repository (optional)"
							className="rounded-md border border-[#dddddd] bg-white px-3 py-2 text-sm text-[#252525]"
						/>
					</label>
					<button
						type="submit"
						className="rounded-lg border border-[#dddddd] px-4 py-2 text-sm text-[#454545] hover:bg-[#f5f5f5]"
					>
						Filter
					</button>
				</form>
			</div>

			{!selectedOrg && (
				<p className="text-sm text-[#727272]">
					Select an organization to view its imported PRs.
				</p>
			)}
			{isLoading && (
				<output className="text-sm text-[#727272]">Loading PR triage…</output>
			)}
			{error && (
				<p role="alert" className="text-sm text-red-700">
					Could not load PR triage:{" "}
					{error instanceof Error ? error.message : "Unknown error"}
				</p>
			)}
			{data?.items.length === 0 && (
				<p className="rounded-xl border border-[#e6e6e6] p-8 text-center text-sm text-[#727272]">
					No imported PRs match these filters. Run PR Triage locally, then
					import its JSON snapshot using the authenticated API.
				</p>
			)}
			{!!data?.items.length && (
				<ul className="space-y-3" aria-label="Imported external pull requests">
					{data.items.map((item) => (
						<TriageRow key={`${item.repo}#${item.number}`} item={item} />
					))}
				</ul>
			)}

			{(data || page > 0) && (
				<div className="flex items-center justify-between gap-3 text-sm text-[#727272]">
					<span>
						Showing {data?.items.length ?? 0} records, offset{" "}
						{data?.offset ?? page * PAGE_SIZE}. Inventory completeness is
						unverified.
					</span>
					<div className="flex items-center gap-2">
						<button
							type="button"
							onClick={() => setPage((p) => Math.max(0, p - 1))}
							disabled={page === 0}
							className="rounded-md border border-[#e4e4e4] p-2 hover:bg-[#f5f5f5] disabled:opacity-30"
							aria-label="Previous page"
						>
							<ChevronLeft className="h-4 w-4" />
						</button>
						<span>Page {page + 1}</span>
						<button
							type="button"
							onClick={() => setPage((p) => p + 1)}
							disabled={!data?.has_more}
							className="rounded-md border border-[#e4e4e4] p-2 hover:bg-[#f5f5f5] disabled:opacity-30"
							aria-label="Next page"
						>
							<ChevronRight className="h-4 w-4" />
						</button>
					</div>
				</div>
			)}
		</section>
	);
}
