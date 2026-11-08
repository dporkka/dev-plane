#!/usr/bin/env node
// Resolve a selected same-repo PR to immutable GitHub-origin head and base SHAs.
// Never use user-supplied SHA values to select a validation target.
import { appendFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

const SHA = /^[a-f0-9]{40}$/i;
const REPOSITORY = /^(?!-)[A-Za-z0-9-]{1,39}(?<!-)\/(?!\.{1,2}$)[A-Za-z0-9_.-]{1,100}$/;
const PR_NUMBER = /^[1-9][0-9]{0,9}$/;

function assertSelection(repo, prNumber) {
  if (typeof repo !== 'string' || !REPOSITORY.test(repo)) throw new Error('Invalid repository identifier');
  if (typeof prNumber !== 'string' || !PR_NUMBER.test(prNumber) || !Number.isSafeInteger(Number(prNumber))) {
    throw new Error('Invalid PR number');
  }
}

export function validateStackPullRequest(pr, { repo, prNumber }) {
  assertSelection(repo, prNumber);
  if (!pr || typeof pr !== 'object' || pr.number !== Number(prNumber)) {
    throw new Error('GitHub PR number does not match requested PR');
  }
  if (pr.state !== 'open') throw new Error('Target PR must be open');
  if (!pr.head || !pr.base) throw new Error('GitHub PR head and base are required');
  if (pr.head.repo?.full_name !== repo || pr.base.repo?.full_name !== repo) {
    throw new Error('Only same-repository PRs may be qualified');
  }
  const headSha = pr.head.sha;
  const baseSha = pr.base.sha;
  if (typeof headSha !== 'string' || !SHA.test(headSha) || !/[1-9a-f]/i.test(headSha)
      || typeof baseSha !== 'string' || !SHA.test(baseSha) || !/[1-9a-f]/i.test(baseSha)) {
    throw new Error('GitHub PR head/base SHA must be a nonzero full commit id');
  }
  if (headSha.toLowerCase() === baseSha.toLowerCase()) throw new Error('Head and base SHA must be distinct');
  return { number: pr.number, headSha, baseSha };
}

export async function resolveStackPullRequest({ repo, prNumber, token, fetchImpl = fetch }) {
  assertSelection(repo, prNumber);
  if (typeof token !== 'string' || !token.trim()) throw new Error('Read-only GitHub token required');
  const response = await fetchImpl('https://api.github.com/repos/' + repo + '/pulls/' + prNumber, {
    method: 'GET',
    headers: {
      Accept: 'application/vnd.github+json',
      Authorization: 'Bearer ' + token,
      'X-GitHub-Api-Version': '2022-11-28',
      'User-Agent': 'dev-plane-stack-qualification',
    },
    redirect: 'error',
    signal: AbortSignal.timeout(20_000),
  });
  if (!response.ok) throw new Error('GitHub API HTTP ' + response.status + ' resolving PR');
  return validateStackPullRequest(await response.json(), { repo, prNumber });
}

// Advisory test execution uses repository-controlled Make/npm commands. If the PR changes
// their definitions, do not report a green result from this unattended test profile.
const VERIFICATION_FILES = new Set([
  'Makefile', '.golangci.yml', '.golangci.yaml', 'go.work', 'go.work.sum',
  'devplane.yaml', 'package.json', 'package-lock.json',
  'apps/web/package.json', 'apps/web/package-lock.json',
  'packages/sdk/typescript/package.json', 'packages/sdk/typescript/package-lock.json',
  'docker-compose.yml', 'docker-compose.yaml', 'compose.yml', 'compose.yaml',
]);
function isVerificationPath(path) {
  return VERIFICATION_FILES.has(path)
    || path.startsWith('.github/workflows/')
    || path.startsWith('.github/actions/')
    || path.startsWith('scripts/');
}

export function assertNoVerificationPlanChanges(files) {
  if (!Array.isArray(files) || files.length === 0) {
    throw new Error('PR changed-file inventory is missing or invalid');
  }
  for (const file of files) {
    for (const path of [file?.filename, file?.previous_filename].filter(x => x !== undefined && x !== null)) {
      if (typeof path !== 'string' || !path || path.startsWith('/')
          || /[\x00-\x1f]/.test(path) || path.split('/').some(p => p === '.' || p === '..')) {
        throw new Error('Invalid file path in PR changed-file inventory');
      }
      if (isVerificationPath(path)) {
        throw new Error('PR changes protected verification plan path: ' + path);
      }
    }
    if (typeof file?.filename !== 'string') throw new Error('Invalid PR changed file');
  }
}

export async function listStackPullFiles({ repo, prNumber, token, fetchImpl = fetch, maxPages = 30 }) {
  assertSelection(repo, prNumber);
  if (typeof token !== 'string' || !token.trim()) throw new Error('Read-only GitHub token required');
  if (!Number.isSafeInteger(maxPages) || maxPages < 1 || maxPages > 30) {
    throw new Error('Invalid changed-file pagination limit');
  }
  const allFiles = [];
  for (let page = 1; page <= maxPages; page++) {
    const url = 'https://api.github.com/repos/' + repo + '/pulls/' + prNumber
      + '/files?per_page=100&page=' + page;
    const response = await fetchImpl(url, {
      method: 'GET',
      headers: {
        Accept: 'application/vnd.github+json',
        Authorization: 'Bearer ' + token,
        'X-GitHub-Api-Version': '2022-11-28',
        'User-Agent': 'dev-plane-stack-qualification',
      },
      redirect: 'error',
      signal: AbortSignal.timeout(20_000),
    });
    if (!response.ok) throw new Error('GitHub API HTTP ' + response.status + ' reading PR files');
    const batch = await response.json();
    if (!Array.isArray(batch) || batch.length > 100) {
      throw new Error('Invalid PR changed-file API response');
    }
    allFiles.push(...batch);
    if (batch.length < 100) return allFiles;
  }
  throw new Error('PR changed-file pagination incomplete; cannot trust verification plan');
}

export async function resolveSafeQualification({ repo, prNumber, token, fetchImpl = fetch }) {
  const before = await resolveStackPullRequest({ repo, prNumber, token, fetchImpl });
  const files = await listStackPullFiles({ repo, prNumber, token, fetchImpl });
  assertNoVerificationPlanChanges(files);
  const after = await resolveStackPullRequest({ repo, prNumber, token, fetchImpl });
  return assertUnchangedStackPullRequest(after, before);
}

/** Reconfirm after testing. A previously passing SHA must never silently become current. */
export function assertUnchangedStackPullRequest(current, expected) {
  if (!current || !expected
      || typeof expected.headSha !== 'string' || !SHA.test(expected.headSha)
      || typeof expected.baseSha !== 'string' || !SHA.test(expected.baseSha)) {
    throw new Error('Expected exact PR head and base SHA must be specified');
  }
  if (current.headSha !== expected.headSha || current.baseSha !== expected.baseSha) {
    throw new Error('PR head/base changed after qualification; tested evidence is stale');
  }
  return current;
}

async function main() {
  if (process.env.GITHUB_EVENT_NAME !== 'workflow_dispatch' || process.env.GITHUB_REF !== 'refs/heads/main') {
    throw new Error('Manual qualification must execute from trusted refs/heads/main');
  }
  const args = process.argv.slice(2);
  if (args.length > 1 || (args.length === 1 && args[0] !== '--reconfirm')) {
    throw new Error('Expected no arguments or --reconfirm');
  }
  // The initial dispatch must verify the complete PR file inventory before exporting
  // any SHA. Finalization only rechecks identity: an unchanged SHA pins the same tree.
  const resolver = args[0] === '--reconfirm' ? resolveStackPullRequest : resolveSafeQualification;
  const pr = await resolver({
    repo: process.env.GITHUB_REPOSITORY,
    prNumber: process.env.PR_NUMBER,
    token: process.env.GH_TOKEN,
  });
  if (args[0] === '--reconfirm') {
    assertUnchangedStackPullRequest(pr, {
      headSha: process.env.EXPECTED_HEAD_SHA,
      baseSha: process.env.EXPECTED_BASE_SHA,
    });
    process.stdout.write('Advisory qualification identity unchanged: #' + pr.number + ' head ' + pr.headSha + ' base ' + pr.baseSha + '\n');
    return;
  }
  if (!process.env.GITHUB_OUTPUT) throw new Error('GITHUB_OUTPUT must be set by GitHub Actions');
  appendFileSync(process.env.GITHUB_OUTPUT, 'head_sha=' + pr.headSha + '\nbase_sha=' + pr.baseSha + '\n');
  process.stdout.write('Read-only PR identity resolved: #' + pr.number + ' head ' + pr.headSha + ' base ' + pr.baseSha + '\n');
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(err => {
    process.stderr.write('Stack PR resolution blocked: ' + err.message + '\n');
    process.exitCode = 1;
  });
}
