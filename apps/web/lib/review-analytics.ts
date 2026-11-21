/** Reporting counts describe PRs created in the selected UTC dates, not merge events. */
export interface ReviewPR {
  id: string;
  repository_id: string;
  status: "open" | "closed" | "merged";
  created_at: string;
  merged_at?: string;
  title?: string;
  number?: number;
  url?: string;
}

/** The API exposes `state` and `merged_at`, whereas the UI uses a canonical status. */
export function normalizeReviewPR(value: unknown): ReviewPR | null {
  if (!value || typeof value !== "object") return null;
  const record = value as Record<string, unknown>;
  if (
    typeof record.id !== "string" ||
    typeof record.repository_id !== "string" ||
    typeof record.created_at !== "string"
  )
    return null;
  const rawState = record.state ?? record.status;
  const status =
    record.merged_at || rawState === "merged"
      ? "merged"
      : rawState === "open"
        ? "open"
        : rawState === "closed"
          ? "closed"
          : null;
  if (!status) return null;
  return {
    id: record.id,
    repository_id: record.repository_id,
    created_at: record.created_at,
    merged_at:
      typeof record.merged_at === "string" ? record.merged_at : undefined,
    status,
    title: typeof record.title === "string" ? record.title : undefined,
    number: typeof record.number === "number" ? record.number : undefined,
    url: typeof record.url === "string" ? record.url : undefined,
  };
}

/** Protect CSV exports from formula injection in untrusted titles and repository names. */
export function csvCell(value: string | number): string {
  const text = String(value);
  const safe = ["=", "+", "-", "@"].includes(text.trimStart().charAt(0))
    ? `'${text}`
    : text;
  return `"${safe.replaceAll('"', '""')}"`;
}

/** Actual create-to-merge median for loaded PRs with valid timestamps. */
export function medianHoursToMerge(prs: ReviewPR[]): number | null {
  const hours = prs
    .filter((pr) => pr.status === "merged" && pr.merged_at)
    .map(
      (pr) =>
        (Date.parse(pr.merged_at!) - Date.parse(pr.created_at)) / 3_600_000,
    )
    .filter((duration) => Number.isFinite(duration) && duration >= 0)
    .sort((a, b) => a - b);
  if (!hours.length) return null;
  const mid = Math.floor(hours.length / 2);
  const median =
    hours.length % 2 ? hours[mid] : (hours[mid - 1] + hours[mid]) / 2;
  return Math.round(median * 10) / 10;
}

export interface ReviewSummary {
  total: number;
  merged: number;
  open: number;
  closed: number;
  prs: ReviewPR[];
}

export function summarizePullRequests(
  prs: ReviewPR[],
  start: string,
  end: string,
  repositoryId: string,
): ReviewSummary {
  const empty: ReviewSummary = {
    total: 0,
    merged: 0,
    open: 0,
    closed: 0,
    prs: [],
  };
  const validDate = /^\d{4}-\d{2}-\d{2}$/;
  if (!validDate.test(start) || !validDate.test(end)) return empty;
  const startMs = Date.parse(`${start}T00:00:00.000Z`);
  const endMs = Date.parse(`${end}T23:59:59.999Z`);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || startMs > endMs)
    return empty;

  const filtered = prs.filter((pr) => {
    const createdMs = Date.parse(pr.created_at);
    return (
      (repositoryId === "all" || pr.repository_id === repositoryId) &&
      Number.isFinite(createdMs) &&
      createdMs >= startMs &&
      createdMs <= endMs
    );
  });

  return {
    total: filtered.length,
    merged: filtered.filter((pr) => pr.status === "merged").length,
    open: filtered.filter((pr) => pr.status === "open").length,
    closed: filtered.filter((pr) => pr.status === "closed").length,
    prs: filtered,
  };
}
