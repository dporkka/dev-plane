import assert from "node:assert/strict";
import test from "node:test";
import {
  type ReviewPR,
  csvCell,
  filterReviewPRs,
  medianHoursToMerge,
  normalizeReviewPR,
  summarizePullRequests,
} from "./review-analytics";

const prs: ReviewPR[] = [
  {
    id: "1",
    repository_id: "a",
    status: "merged",
    created_at: "2026-11-01T12:00:00Z",
  },
  {
    id: "2",
    repository_id: "a",
    status: "open",
    created_at: "2026-11-20T10:00:00Z",
  },
  {
    id: "3",
    repository_id: "b",
    status: "closed",
    created_at: "2026-11-21T03:00:00Z",
  },
  {
    id: "4",
    repository_id: "b",
    status: "merged",
    created_at: "2026-10-20T12:00:00Z",
  },
];

test("counts only PRs created within the inclusive reporting window", () => {
  const result = summarizePullRequests(prs, "2026-11-01", "2026-11-20", "all");
  assert.equal(result.total, 2);
  assert.equal(result.merged, 1);
  assert.equal(result.open, 1);
  assert.equal(result.closed, 0);
});

test("repository selection applies to every status count", () => {
  const result = summarizePullRequests(prs, "2026-11-01", "2026-11-21", "b");
  assert.equal(result.total, 1);
  assert.equal(result.closed, 1);
  assert.equal(result.merged, 0);
});

test("normalizes the API state field and merged_at timestamp instead of assuming status", () => {
  const merged = normalizeReviewPR({
    id: "pr-1",
    repository_id: "a",
    state: "closed",
    merged_at: "2026-11-14T00:00:00Z",
    created_at: "2026-11-01T00:00:00Z",
  });
  assert.equal(merged?.status, "merged");
  assert.equal(merged?.merged_at, "2026-11-14T00:00:00Z");
  assert.equal(normalizeReviewPR({ id: "bad", state: "open" }), null);
});

test("computes median elapsed hours only from PRs with valid merge timestamps", () => {
  const one = normalizeReviewPR({
    id: "1",
    repository_id: "a",
    state: "closed",
    created_at: "2026-11-01T00:00:00Z",
    merged_at: "2026-11-01T02:00:00Z",
  });
  const two = normalizeReviewPR({
    id: "2",
    repository_id: "a",
    state: "merged",
    created_at: "2026-11-01T00:00:00Z",
    merged_at: "2026-11-01T06:00:00Z",
  });
  assert.equal(medianHoursToMerge([one!, two!]), 4);
  assert.equal(medianHoursToMerge(prs), null);
});

test("CSV export neutralizes spreadsheet formulas in untrusted PR titles", () => {
  assert.equal(
    csvCell('=HYPERLINK("https://evil")'),
    '"\'=HYPERLINK(""https://evil"")"',
  );
  assert.equal(csvCell('ordinary "PR"'), '"ordinary ""PR"""');
});

test("empty, invalid, and reversed ranges never display fabricated totals", () => {
  assert.equal(
    summarizePullRequests([], "2026-11-01", "2026-11-20", "all").total,
    0,
  );
  assert.equal(
    summarizePullRequests(prs, "invalid", "2026-11-20", "all").total,
    0,
  );
  assert.equal(
    summarizePullRequests(prs, "2026-11-21", "2026-11-01", "all").total,
    0,
  );
});

test("preserves linked run, task, and draft state for triage details", () => {
  const record = normalizeReviewPR({
    id: "5",
    repository_id: "a",
    state: "open",
    created_at: "2026-11-15T00:00:00Z",
    run_id: "run-123",
    task_id: "task-456",
    draft: true,
    branch: "feature",
    base_branch: "main",
    body: "Change summary",
  });
  assert.equal(record?.run_id, "run-123");
  assert.equal(record?.task_id, "task-456");
  assert.equal(record?.draft, true);
  assert.equal(record?.branch, "feature");
});

test("triage filters by status, repository, text and orders newest first", () => {
  const candidates = [
    {
      id: "old",
      repository_id: "a",
      status: "open" as const,
      created_at: "2026-11-01T00:00:00Z",
      title: "Patch auth",
    },
    {
      id: "new",
      repository_id: "a",
      status: "open" as const,
      created_at: "2026-11-20T00:00:00Z",
      title: "Patch auth session",
    },
    {
      id: "merged",
      repository_id: "b",
      status: "merged" as const,
      created_at: "2026-11-21T00:00:00Z",
      title: "Patch auth",
    },
  ];
  const filtered = filterReviewPRs(
    candidates,
    {
      status: "open",
      repositoryId: "a",
      search: "auth",
    },
    new Map([
      ["a", "team/auth"],
      ["b", "team/core"],
    ]),
  );
  assert.deepEqual(
    filtered.map((pr) => pr.id),
    ["new", "old"],
  );
  assert.deepEqual(
    filterReviewPRs(
      candidates,
      {
        status: "all",
        repositoryId: "all",
        search: "team/core",
      },
      new Map([
        ["a", "team/auth"],
        ["b", "team/core"],
      ]),
    ).map((pr) => pr.id),
    ["merged"],
  );
});
