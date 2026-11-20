import assert from 'node:assert/strict';
import test from 'node:test';
import {
  classifyChecks,
  collectPortfolio,
  formatMarkdown,
  listRecentPulls,
  reportForRepository,
  validateRepositories,
} from './pr-queue-report.mjs';

const PR = (number, state, draft, created_at, extra = {}) => ({
  number, state, draft, created_at,
  updated_at: created_at,
  merged_at: null,
  html_url: `https://github.com/acme/a/pull/${number}`,
  head: { sha: `sha-${number}` },
  ...extra,
});
const now = new Date('2026-10-18T12:00:00Z');

test('classifies a cohort without mistaking open drafts or abandoned PRs for delivered changes', () => {
  const pulls = [
    PR(1, 'open', true, '2026-10-11T12:00:00Z'),
    PR(2, 'open', false, '2026-10-14T12:00:00Z'),
    PR(3, 'closed', false, '2026-10-15T12:00:00Z', { merged_at: '2026-10-16T12:00:00Z' }),
    PR(4, 'closed', false, '2026-10-16T12:00:00Z'),
  ];
  const r = reportForRepository('acme/a', pulls, { now, staleDays: 3 });
  assert.deepEqual(r.counts, { created: 4, merged: 1, open: 2, draft: 1, closedUnmerged: 1, staleOpen: 2 });
  assert.deepEqual(r.priority.map(p => p.number), [2, 1]);
  assert.equal(r.priority[0].ci, 'not-inspected');
});

test('missing check runs and statuses are not reported as passing', () => {
  assert.equal(classifyChecks([], []), 'no-evidence');
  assert.equal(classifyChecks([{ status: 'in_progress', conclusion: null }], []), 'pending');
  assert.equal(classifyChecks([{ status: 'completed', conclusion: 'failure' }], []), 'failed');
  assert.equal(classifyChecks([{ status: 'completed', conclusion: 'success' }], [{ state: 'failure' }]), 'failed');
  assert.equal(classifyChecks([{ status: 'completed', conclusion: 'success' }], []), 'passing-observed-only');
  assert.equal(classifyChecks([], [{ state: 'success' }]), 'passing-observed-only');
  assert.equal(classifyChecks([{ status: 'completed', conclusion: 'skipped' }], []), 'incomplete');
});

test('pagination continues past 100 pulls and stops on older rows', async () => {
  const first = Array.from({ length: 100 }, (_, i) => PR(i + 1, 'open', true, '2026-10-17T12:00:00Z'));
  const second = [PR(101, 'open', false, '2026-10-13T12:00:00Z'), PR(102, 'open', false, '2026-10-01T12:00:00Z')];
  const requested = [];
  const pulls = await listRecentPulls('acme/a', async p => { requested.push(p); return requested.length === 1 ? first : second; }, { since: new Date('2026-10-11T00:00:00Z'), maxPages: 3 });
  assert.equal(pulls.length, 101);
  assert.equal(requested.length, 2);
});

test('pagination fails closed when max page cap could hide cohort PRs', async () => {
  await assert.rejects(listRecentPulls('acme/a', async () => Array.from({ length: 100 }, (_, i) => PR(i + 1, 'open', true, '2026-10-17T12:00:00Z')), { since: new Date('2026-10-11T00:00:00Z'), maxPages: 1 }), /incomplete/);
});

test('invalid repository inputs are rejected before network access', () => {
  assert.deepEqual(validateRepositories('acme/a,other/b'), ['acme/a', 'other/b']);
  assert.throws(() => validateRepositories('acme/a,https://example.com'), /Invalid repository/);
  assert.throws(() => validateRepositories('acme/a,acme/a'), /Duplicate repository/);
});

test('repository report can account for old open PRs outside the creation cohort', () => {
  const recent = [PR(7, 'closed', false, '2026-10-17T12:00:00Z', { merged_at: '2026-10-17T12:01:00Z' })];
  const allOpen = [PR(3, 'open', true, '2026-09-01T12:00:00Z'), PR(5, 'open', false, '2026-10-15T12:00:00Z')];
  const r = reportForRepository('acme/a', recent, { now, staleDays: 3, allOpen });
  assert.equal(r.counts.created, 1);
  assert.equal(r.counts.merged, 1);
  assert.equal(r.inventory.open, 2);
  assert.equal(r.inventory.draft, 1);
  assert.equal(r.inventory.stale, 2);
  assert.deepEqual(r.priority.map(p => p.number), [5, 3]);
});

test('rejects dot segments and invalid GitHub owner names rather than rewriting API paths', () => {
  assert.throws(() => validateRepositories('../..'), /Invalid repository/);
  assert.throws(() => validateRepositories('acme/..'), /Invalid repository/);
  assert.throws(() => validateRepositories('-acme/myrepo'), /Invalid repository/);
});

test('portfolio report inspects exact-head CI only for selected work and labels green as advisory', async () => {
  const visited = [];
  const getJson = async path => {
    visited.push(path);
    if (path.includes('state=all')) return [PR(1, 'open', false, '2026-10-17T12:00:00Z')];
    if (path.includes('state=open')) return [PR(1, 'open', false, '2026-10-17T12:00:00Z')];
    if (path.includes('/check-runs')) return { total_count: 1, check_runs: [{ status: 'completed', conclusion: 'success' }] };
    if (path.endsWith('/pulls/1')) return PR(1, 'open', false, '2026-10-17T12:00:00Z');
    if (path.endsWith('/status')) return { statuses: [] };
    throw new Error('unexpected path ' + path);
  };
  const data = await collectPortfolio(['acme/a'], getJson, { now, inspect: 1 });
  assert.equal(data.reports[0].priority[0].ci, 'passing-observed-only');
  assert.equal(data.reports[0].priority[0].ciTriage, 'verify-required-gates');
  assert.deepEqual(data.reports[0].priority[0].failedChecks, []);
  assert(visited.some(p => p.includes('/commits/sha-1/check-runs')));
  assert(visited.some(p => p.endsWith('/pulls/1')));
  const md = formatMarkdown(data);
  assert.match(md, /Passing observed checks do not certify required contexts/i);
  assert.match(md, /passing-observed-only/);
});

test('GitHub evidence fetch failure does not emit a partial success report', async () => {
  const getJson = async path => {
    if (path.includes('/pulls?')) return [PR(1, 'open', false, '2026-10-17T12:00:00Z')];
    throw new Error('GitHub API HTTP 403');
  };
  await assert.rejects(collectPortfolio(['acme/a'], getJson, { now, inspect: 1 }), /403/);
});

test('more than one page of check runs cannot be misrepresented as passing', async () => {
  const getJson = async path => {
    if (path.includes('/pulls?')) return [PR(1, 'open', false, '2026-10-17T12:00:00Z')];
    if (path.includes('/check-runs')) return { total_count: 101, check_runs: [{ status: 'completed', conclusion: 'success' }] };
    if (path.endsWith('/pulls/1')) return PR(1, 'open', false, '2026-10-17T12:00:00Z');
    return { statuses: [] };
  };
  const data = await collectPortfolio(['acme/a'], getJson, { now, inspect: 1 });
  assert.equal(data.reports[0].priority[0].ci, 'incomplete');
});

test('a push during CI inspection invalidates the entire PR snapshot', async () => {
  const getJson = async path => {
    if (path.includes('/pulls?')) return [PR(1, 'open', false, '2026-10-17T12:00:00Z')];
    if (path.includes('/check-runs')) return { total_count: 1, check_runs: [{ name: 'Test', status: 'completed', conclusion: 'success', head_sha: 'sha-1' }] };
    if (path.endsWith('/status')) return { statuses: [] };
    if (path.endsWith('/pulls/1')) return PR(1, 'open', false, '2026-10-17T12:00:00Z', { head: { sha: 'new-head' } });
    throw new Error('unexpected path ' + path);
  };
  await assert.rejects(collectPortfolio(['acme/a'], getJson, { now, inspect: 1 }), /head changed/);
});

test('a PR closed during inspection is not presented as a current open candidate', async () => {
  const getJson = async path => {
    if (path.includes('/pulls?')) return [PR(1, 'open', false, '2026-10-17T12:00:00Z')];
    if (path.includes('/check-runs')) return { total_count: 0, check_runs: [] };
    if (path.endsWith('/status')) return { statuses: [] };
    if (path.endsWith('/pulls/1')) return PR(1, 'closed', false, '2026-10-17T12:00:00Z');
    throw new Error('unexpected path ' + path);
  };
  await assert.rejects(collectPortfolio(['acme/a'], getJson, { now, inspect: 1 }), /no longer open/);
});

test('CI triage exposes evidence without claiming a verified root cause', async () => {
  const getJson = async path => {
    if (path.includes('/pulls?')) return [PR(1, 'open', false, '2026-10-17T12:00:00Z')];
    if (path.includes('/check-runs')) return { total_count: 1, check_runs: [{ name: 'Format and audit', status: 'completed', conclusion: 'startup_failure', head_sha: 'sha-1' }] };
    if (path.endsWith('/status')) return { statuses: [] };
    if (path.endsWith('/pulls/1')) return PR(1, 'open', false, '2026-10-17T12:00:00Z');
    throw new Error('unexpected path ' + path);
  };
  const data = await collectPortfolio(['acme/a'], getJson, { now, inspect: 1 });
  const candidate = data.reports[0].priority[0];
  assert.equal(candidate.ci, 'failed');
  assert.equal(candidate.ciTriage, 'investigate-runner-admission');
  assert.deepEqual(candidate.failedChecks, [{name:'Format and audit',conclusion:'startup_failure',source:'check-run'}]);
  assert.match(formatMarkdown(data), /failing contexts \(not root causes\): Format and audit/);
});
