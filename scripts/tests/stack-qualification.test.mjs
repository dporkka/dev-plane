import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { validateStackPullRequest, resolveStackPullRequest, assertUnchangedStackPullRequest, assertNoVerificationPlanChanges, listStackPullFiles, resolveSafeQualification } from '../ci/resolve-stack-pr.mjs';

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

test('rejects an advisory success when head or base moves during verification', () => {
  const current = { number: 202, headSha: head, baseSha: base };
  assert.deepEqual(assertUnchangedStackPullRequest(current, { headSha: head, baseSha: base }), current);
  assert.throws(() => assertUnchangedStackPullRequest(
    { ...current, headSha: 'c'.repeat(40) }, { headSha: head, baseSha: base }
  ), /changed|stale/i);
  assert.throws(() => assertUnchangedStackPullRequest(
    { ...current, baseSha: 'd'.repeat(40) }, { headSha: head, baseSha: base }
  ), /changed|stale/i);
  assert.throws(() => assertUnchangedStackPullRequest(current, { headSha: '', baseSha: base }), /expected|sha/i);
});

test('a separate trusted read-only final job checks for PR movement after tests', () => {
  const workflow = readFileSync(new URL('../../.github/workflows/stack-qualification.yml', import.meta.url), 'utf8');
  assert.match(workflow, /finalize:\s*\n\s*name:.*\n\s*needs:\s*\[resolve, qualify\]/);
  assert.match(workflow, /EXPECTED_HEAD_SHA:\s*\$\{\{ needs\.resolve\.outputs\.head_sha \}\}/);
  assert.match(workflow, /EXPECTED_BASE_SHA:\s*\$\{\{ needs\.resolve\.outputs\.base_sha \}\}/);
  assert.match(workflow, /node scripts\/ci\/resolve-stack-pr\.mjs --reconfirm/);
  assert.match(workflow, /ref: \$\{\{ github\.sha \}\}/);
});

test('rejects PRs that modify their own verification plan, including renames', () => {
  const protectedPaths = [
    'Makefile', '.github/workflows/ci.yml', '.github/actions/test/action.yml',
    'scripts/ci/make-ci.mjs', 'go.work', 'devplane.yaml',
    'apps/web/package.json', 'apps/web/package-lock.json',
    'packages/sdk/typescript/package.json', '.golangci.yml',
    'docker-compose.yml',
  ];
  for (const path of protectedPaths) {
    assert.throws(() => assertNoVerificationPlanChanges([{ filename: path }]), /verification plan|protected/i, path);
    assert.throws(() => assertNoVerificationPlanChanges([
      { filename: 'docs/renamed-' + path.replaceAll('/', '_'), previous_filename: path },
    ]), /verification plan|protected/i, path);
  }
  assert.doesNotThrow(() => assertNoVerificationPlanChanges([
    { filename: 'packages/repoprotocol/protocol.go', status: 'modified' },
    { filename: 'packages/repoprotocol/protocol_test.go', status: 'added' },
  ]));
  assert.throws(() => assertNoVerificationPlanChanges([{}]), /file|invalid/i);
});

test('changed file inspection fully paginates, rejects truncated inventory, and denies API errors', async () => {
  const pages = [];
  const files = await listStackPullFiles({
    repo, prNumber: '202', token: 'fake-token',
    fetchImpl: async url => {
      pages.push(url);
      const page = Number(new URL(url).searchParams.get('page'));
      return { ok: true, json: async () => page === 1
        ? Array.from({ length: 100 }, (_, i) => ({ filename: 'packages/src/file' + i + '.go' }))
        : [{ filename: 'packages/src/last.go' }] };
    },
  });
  assert.equal(files.length, 101);
  assert.equal(pages.length, 2);
  assert.equal(new URL(pages[0]).pathname, '/repos/dporkka/dev-plane/pulls/202/files');
  await assert.rejects(listStackPullFiles({
    repo, prNumber: '202', token: 'fake-token',
    fetchImpl: async () => ({ ok: false, status: 403 }),
  }), /HTTP 403/);
  await assert.rejects(listStackPullFiles({
    repo, prNumber: '202', token: 'fake-token', maxPages: 1,
    fetchImpl: async () => ({ ok: true, json: async () =>
      Array.from({ length: 100 }, (_, i) => ({ filename: 'x' + i })) }),
  }), /incomplete|pagination/i);
});

test('trusted qualification reads PR twice and denies head drift or modified verification commands', async () => {
  const seen = [];
  const resolver = async url => {
    seen.push(url);
    if (url.includes('/files?')) return { ok: true, json: async () => [{ filename: 'packages/repoprotocol/protocol.go' }] };
    return { ok: true, json: async () => fixture() };
  };
  const result = await resolveSafeQualification({ repo, prNumber: '202', token: 'fake-token', fetchImpl: resolver });
  assert.equal(result.headSha, head);
  assert.equal(seen.filter(url => url.endsWith('/pulls/202')).length, 2);
  let prReads = 0;
  const moved = async url => {
    if (url.includes('/files?')) return { ok: true, json: async () => [{ filename: 'packages/repoprotocol/protocol.go' }] };
    prReads++;
    return { ok: true, json: async () => fixture(prReads === 2 ? { head: { sha: 'c'.repeat(40), repo: { full_name: repo } } } : {}) };
  };
  await assert.rejects(resolveSafeQualification({ repo, prNumber: '202', token: 'fake-token', fetchImpl: moved }), /stale|changed/i);
  await assert.rejects(resolveSafeQualification({
    repo, prNumber: '202', token: 'fake-token',
    fetchImpl: async url => ({ ok: true, json: async () => url.includes('/files?')
      ? [{ filename: 'Makefile' }] : fixture() }),
  }), /verification plan|protected/i);
});

test('manual qualification command uses verified file inventory before publishing SHA output', () => {
  const script = readFileSync(new URL('../ci/resolve-stack-pr.mjs', import.meta.url), 'utf8');
  assert.match(script, /const resolver = args\[0\] === '--reconfirm' \? resolveStackPullRequest : resolveSafeQualification;/);
  assert.match(script, /assertNoVerificationPlanChanges\(files\)/);
  assert.match(script, /assertUnchangedStackPullRequest\(after, before\)/);
});
