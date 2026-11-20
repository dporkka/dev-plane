#!/usr/bin/env node
// Read-only, fail-closed portfolio PR inventory. No repository mutations.
import { analyzeCheckEvidence, assertCurrentPRHead } from './pr-ci-evidence.mjs';
export { classifyChecks } from './pr-ci-evidence.mjs';
const DEFAULT_REPOS = 'dporkka/adacavo,nulang-org/nulang,dporkka/nulang-cloud,dporkka/dev-plane';
const PAGE_SIZE = 100;
const DAY_MS = 24 * 60 * 60 * 1000;

export function validateRepositories(value) {
  const repos = value.split(',').map(s => s.trim());
  const seen = new Set();
  const valid = s => /^(?!-)[A-Za-z0-9-]{1,39}(?<!-)\/(?!\.\.?$)[A-Za-z0-9_.-]{1,100}$/.test(s);
  if (repos.length === 0 || repos.some(s => !valid(s))) {
    throw new Error('Invalid repository list; expected owner/name comma-separated');
  }
  for (const repo of repos) {
    if (seen.has(repo)) throw new Error(`Duplicate repository: ${repo}`);
    seen.add(repo);
  }
  return repos;
}

function validateArray(value, path) {
  if (!Array.isArray(value)) throw new Error(`Unexpected GitHub response at ${path}: array required`);
  return value;
}

async function listPulls(repo, getJson, { since = null, maxPages = 20, state = 'all' } = {}) {
  if (!Number.isSafeInteger(maxPages) || maxPages < 1) throw new Error('Invalid maxPages');
  const items = [];
  for (let page = 1; page <= maxPages; page++) {
    const path = `/repos/${repo}/pulls?state=${state}&sort=created&direction=desc&per_page=${PAGE_SIZE}&page=${page}`;
    const pageItems = validateArray(await getJson(path), path);
    let exhaustedByDate = false;
    for (const pr of pageItems) {
      if (!pr || !Number.isInteger(pr.number) || !pr.created_at || !pr.head?.sha) {
        throw new Error(`Incomplete PR metadata at ${path}`);
      }
      if (since && Date.parse(pr.created_at) < since.getTime()) {
        exhaustedByDate = true;
        break;
      }
      items.push(pr);
    }
    if (exhaustedByDate || pageItems.length < PAGE_SIZE) return items;
  }
  throw new Error(`PR inventory incomplete for ${repo}; maxPages limit reached (increase --max-pages)`);
}

export function listRecentPulls(repo, getJson, { since, maxPages = 20 }) {
  if (!(since instanceof Date) || Number.isNaN(since.getTime())) throw new Error('Valid since date required');
  return listPulls(repo, getJson, { since, maxPages, state: 'all' });
}

export function listOpenPulls(repo, getJson, { maxPages = 20 } = {}) {
  return listPulls(repo, getJson, { maxPages, state: 'open' });
}

function stale(pr, now, staleDays) {
  const updated = Date.parse(pr.updated_at);
  if (!Number.isFinite(updated)) throw new Error(`Invalid updated_at for PR #${pr.number}`);
  return now.getTime() - updated >= staleDays * DAY_MS;
}

export function reportForRepository(repo, recent, { now = new Date(), staleDays = 3, allOpen = null } = {}) {
  const open = allOpen ?? recent.filter(p => p.state === 'open');
  const counts = {
    created: recent.length,
    merged: recent.filter(p => Boolean(p.merged_at)).length,
    open: recent.filter(p => p.state === 'open').length,
    draft: recent.filter(p => p.state === 'open' && p.draft).length,
    closedUnmerged: recent.filter(p => p.state === 'closed' && !p.merged_at).length,
    staleOpen: recent.filter(p => p.state === 'open' && stale(p, now, staleDays)).length,
  };
  const inventory = {
    open: open.length,
    draft: open.filter(p => p.draft).length,
    stale: open.filter(p => stale(p, now, staleDays)).length,
  };
  const priority = open.slice().sort((a, b) => Number(a.draft) - Number(b.draft)
    || Date.parse(a.updated_at) - Date.parse(b.updated_at)
    || a.number - b.number).map(pr => ({
    number: pr.number, draft: Boolean(pr.draft), url: pr.html_url,
    head: pr.head.sha, updatedAt: pr.updated_at, ci: 'not-inspected',
  }));
  return { repo, counts, inventory, priority };
}

async function getEvidence(repo, pr, getJson) {
  const base = `/repos/${repo}/commits/${encodeURIComponent(pr.head)}`;
  const checkRuns = await getJson(`${base}/check-runs?per_page=100`);
  const statuses = await getJson(`${base}/status`);
  if (!Array.isArray(checkRuns?.check_runs) || !Array.isArray(statuses?.statuses)) {
    throw new Error(`Incomplete check/status evidence for ${repo} #${pr.number}`);
  }
  const evidence = analyzeCheckEvidence(checkRuns.check_runs, statuses.statuses, {
    expectedHead: pr.head, totalCount: checkRuns.total_count,
  });
  // A PR can receive a new push after its inventory listing. Reject this
  // snapshot rather than associating old-commit checks with the new head.
  const current = await getJson(`/repos/${repo}/pulls/${pr.number}`);
  assertCurrentPRHead(current, pr.head);
  return evidence;
}

export async function collectPortfolio(repos, getJson, { now = new Date(), days = 7, staleDays = 3, maxPages = 20, inspect = 5 } = {}) {
  const since = new Date(now.getTime() - days * DAY_MS);
  const reports = [];
  for (const repo of repos) {
    // Both queries must complete. Incomplete pages are fatal, never silently undercounted.
    const recent = await listRecentPulls(repo, getJson, { since, maxPages });
    const allOpen = await listOpenPulls(repo, getJson, { maxPages });
    const report = reportForRepository(repo, recent, { allOpen, now, staleDays });
    for (const pr of report.priority.slice(0, inspect)) {
      const evidence = await getEvidence(repo, pr, getJson);
      pr.ci = evidence.status;
      pr.ciTriage = evidence.triage;
      pr.failedChecks = evidence.failures;
    }
    reports.push(report);
  }
  return { generatedAt: now.toISOString(), since: since.toISOString(),
    caveat: 'Read-only snapshot, checks are advisory. Passing observed checks do not certify required contexts, reviews, merged state, or deployment.',
    reports };
}

export function formatMarkdown(data, { show = 10 } = {}) {
  const lines = [
    '# PR integration queue',
    `Snapshot: ${data.generatedAt}. Creation cohort since ${data.since}.`,
    `**Evidence limitation:** ${data.caveat}`,
    '',
    '| Repository | Created in cohort | Merged | Open in cohort | Closed unmerged | All open | Draft open | Stale open |',
    '|---|---:|---:|---:|---:|---:|---:|---:|',
  ];
  for (const r of data.reports) {
    lines.push(`| ${r.repo} | ${r.counts.created} | ${r.counts.merged} | ${r.counts.open} | ${r.counts.closedUnmerged} | ${r.inventory.open} | ${r.inventory.draft} | ${r.inventory.stale} |`);
  }
  lines.push('');
  for (const r of data.reports) {
    lines.push(`## ${r.repo}`, 'Priority queue (non-draft first, then oldest updates). No merge authorization implied.', '');
    for (const p of r.priority.slice(0, show)) {
      const failing = (p.failedChecks ?? []).slice(0, 3).map(x =>
        x.name.replace(/[^A-Za-z0-9 _./:-]/g, '_').slice(0, 80)).join(', ');
      lines.push(`- [#${p.number}](${p.url}) ${p.draft ? 'draft' : 'review'} | updated ${p.updatedAt} | head \`${p.head.slice(0,12)}\` | CI: ${p.ci}${p.ciTriage ? ` | triage: ${p.ciTriage}` : ''}${failing ? ` | failing contexts (not root causes): ${failing}` : ''}`);
    }
    if (!r.priority.length) lines.push('No open PRs.');
    lines.push('');
  }
  return lines.join('\n');
}

async function apiGet(path) {
  const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN;
  const response = await fetch(`https://api.github.com${path}`, {
    headers: {
      Accept: 'application/vnd.github+json',
      'X-GitHub-Api-Version': '2022-11-28',
      'User-Agent': 'dev-plane-pr-queue-report',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    signal: AbortSignal.timeout(20000),
  });
  if (!response.ok) throw new Error(`GitHub API HTTP ${response.status} for ${path} (no partial report emitted)`);
  return response.json();
}

function positiveNumber(value, key, { allowZero = false } = {}) {
  const n = Number(value);
  if (!Number.isSafeInteger(n) || n < (allowZero ? 0 : 1)) throw new Error(`Invalid ${key}`);
  return n;
}

async function main(argv) {
  const opts = {};
  for (let i = 0; i < argv.length; i++) {
    const k = argv[i];
    if (!['--repos', '--days', '--stale-days', '--max-pages', '--inspect', '--show', '--format'].includes(k) || !argv[i + 1]) {
      throw new Error(`Invalid argument ${k}; expected --repos/--days/--stale-days/--max-pages/--inspect/--show/--format followed by value`);
    }
    opts[k] = argv[++i];
  }
  const repos = validateRepositories(opts['--repos'] ?? DEFAULT_REPOS);
  const days = positiveNumber(opts['--days'] ?? 7, 'days');
  const staleDays = positiveNumber(opts['--stale-days'] ?? 3, 'stale-days');
  const maxPages = positiveNumber(opts['--max-pages'] ?? 20, 'max-pages');
  const inspect = positiveNumber(opts['--inspect'] ?? 5, 'inspect', { allowZero: true });
  const show = positiveNumber(opts['--show'] ?? 10, 'show', { allowZero: true });
  const format = opts['--format'] ?? 'markdown';
  if (!['markdown', 'json'].includes(format)) throw new Error('Format must be markdown or json');
  const data = await collectPortfolio(repos, apiGet, { days, staleDays, maxPages, inspect });
  process.stdout.write((format === 'json' ? JSON.stringify(data, null, 2) : formatMarkdown(data, { show })) + '\n');
}

if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  main(process.argv.slice(2)).catch(err => {
    process.stderr.write(`PR queue report failed: ${err.message}\n`);
    process.exitCode = 1;
  });
}
