import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { validateStackPullRequest, resolveStackPullRequest } from '../ci/resolve-stack-pr.mjs';

const head = 'a'.repeat(40);
const base = 'b'.repeat(40);
const repo = 'dporkka/dev-plane';
const fixture = (overrides = {}) => ({
  number: 202,
  state: 'open',
  draft: true,
  head: { sha: head, repo: { full_name: repo } },
  base: { sha: base, repo: { full_name: repo } },
  ...overrides,
});

test('resolves a same-repo draft PR to exact GitHub-sourced head and base commits', () => {
  assert.deepEqual(validateStackPullRequest(fixture(), { repo, prNumber: '202' }), {
    number: 202,
    headSha: head,
    baseSha: base,
  });
});

test('rejects external/fork PRs, missing heads, changed identity and closed PRs', () => {
  assert.throws(() => validateStackPullRequest(fixture({ state: 'closed' }), { repo, prNumber: '202' }), /open/i);
  assert.throws(() => validateStackPullRequest(fixture({ number: 201 }), { repo, prNumber: '202' }), /number/i);
  assert.throws(() => validateStackPullRequest(fixture({ head: { sha: head, repo: { full_name: 'other/repo' } } }), { repo, prNumber: '202' }), /same.repository/i);
  assert.throws(() => validateStackPullRequest(fixture({ head: null }), { repo, prNumber: '202' }), /head/i);
  assert.throws(() => validateStackPullRequest(fixture({ base: { sha: base, repo: null } }), { repo, prNumber: '202' }), /same.repository/i);
});

test('rejects malicious PR inputs, malformed or identical commit ids', () => {
  assert.throws(() => validateStackPullRequest(fixture(), { repo, prNumber: '202;echo injected' }), /number/i);
  assert.throws(() => validateStackPullRequest(fixture(), { repo: '../evil', prNumber: '202' }), /repository/i);
  assert.throws(() => validateStackPullRequest(fixture({ head: { sha: 'short', repo: { full_name: repo } } }), { repo, prNumber: '202' }), /sha/i);
  assert.throws(() => validateStackPullRequest(fixture({ base: { sha: head, repo: { full_name: repo } } }), { repo, prNumber: '202' }), /distinct/i);
});

test('reads only the canonical GitHub PR endpoint and fails closed on HTTP errors', async () => {
  const requests = [];
  const resolved = await resolveStackPullRequest({
    repo, prNumber: '202', token: 'fake-test-token',
    fetchImpl: async (url, options) => {
      requests.push({ url, options });
      return { ok: true, json: async () => fixture() };
    },
  });
  assert.equal(resolved.headSha, head);
  assert.deepEqual(requests.map(r => r.url), ['https://api.github.com/repos/' + repo + '/pulls/202']);
  assert.equal(requests[0].options.redirect, 'error');
  assert.equal(requests[0].options.headers.Authorization, 'Bearer fake-test-token');
  await assert.rejects(resolveStackPullRequest({
    repo, prNumber: '202', token: 'fake-test-token',
    fetchImpl: async () => ({ ok: false, status: 401 }),
  }), /GitHub API HTTP 401/);
});

test('manual stack workflow is a read-only dispatch from trusted main with guarded exact head', () => {
  const workflow = readFileSync(new URL('../../.github/workflows/stack-qualification.yml', import.meta.url), 'utf8');
  assert.match(workflow, /^  workflow_dispatch:/m);
  assert.match(workflow, /pr_number:\s*\n\s*description:.*\n\s*required: true/);
  assert.doesNotMatch(workflow, /^  pull_request:|^  push:/m);
  assert.match(workflow, /permissions:\s*\n\s*contents: read\s*\n\s*pull-requests: read/);
  assert.match(workflow, /DISPATCH_REF: \$\{\{ github\.ref \}\}/);
  assert.match(readFileSync(new URL('../ci/resolve-stack-pr.mjs', import.meta.url), 'utf8'), /GITHUB_REF !== 'refs\/heads\/main'/);
  assert.match(workflow, /node scripts\/ci\/resolve-stack-pr\.mjs/);
  assert.match(workflow, /needs\.resolve\.outputs\.head_sha/);
  assert.match(workflow, /needs\.resolve\.outputs\.base_sha/);
  assert.match(workflow, /git merge-base --is-ancestor/);
  assert.match(workflow, /make test/);
  assert.match(workflow, /make lint-go/);
  assert.match(workflow, /make build/);
  assert.doesNotMatch(workflow, /pull_request_target:|permissions:\s*write-all|persist-credentials: true/);
});
