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

async function main() {
  if (process.env.GITHUB_EVENT_NAME !== 'workflow_dispatch' || process.env.GITHUB_REF !== 'refs/heads/main') {
    throw new Error('Manual qualification must execute from trusted refs/heads/main');
  }
  const pr = await resolveStackPullRequest({
    repo: process.env.GITHUB_REPOSITORY,
    prNumber: process.env.PR_NUMBER,
    token: process.env.GH_TOKEN,
  });
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
