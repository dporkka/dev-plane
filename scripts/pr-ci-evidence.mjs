// Read-only CI observations. These never authorize merging or deployment.
const FAILURE = new Set(['failure', 'cancelled', 'timed_out', 'action_required', 'startup_failure', 'error']);

export function classifyChecks(checkRuns, statuses) {
  if (!Array.isArray(checkRuns) || !Array.isArray(statuses)) throw new Error('CI evidence must be arrays');
  if (checkRuns.length + statuses.length === 0) return 'no-evidence';
  if (checkRuns.some(x => FAILURE.has(x.conclusion)) || statuses.some(x => FAILURE.has(x.state))) return 'failed';
  if (checkRuns.some(x => x.status !== 'completed') || statuses.some(x => x.state === 'pending')) return 'pending';
  if (checkRuns.some(x => x.conclusion !== 'success') || statuses.some(x => x.state !== 'success')) return 'incomplete';
  return 'passing-observed-only';
}

export function assertCurrentPRHead(pr, expectedHead) {
  if (!pr || !Number.isInteger(pr.number) || !pr.head?.sha || !pr.state) {
    throw new Error('Incomplete PR metadata during CI revalidation');
  }
  if (pr.head.sha !== expectedHead) {
    throw new Error(`PR #${pr.number} head changed during CI inspection: expected ${expectedHead}, got ${pr.head.sha}`);
  }
  if (pr.state !== 'open') throw new Error(`PR #${pr.number} is no longer open during CI inspection`);
}

export function analyzeCheckEvidence(checkRuns, statuses, { expectedHead, totalCount } = {}) {
  if (!Array.isArray(checkRuns) || !Array.isArray(statuses)) throw new Error('CI evidence must be arrays');
  for (const check of checkRuns) {
    if (!check || typeof check !== 'object' || typeof check.status !== 'string') {
      throw new Error('Malformed check-run evidence');
    }
    if (check.head_sha && check.head_sha !== expectedHead) {
      throw new Error(`CI head mismatch: expected ${expectedHead}, got ${check.head_sha}`);
    }
  }
  for (const status of statuses) {
    if (!status || typeof status !== 'object' || typeof status.state !== 'string') {
      throw new Error('Malformed commit-status evidence');
    }
  }
  const failures = [
    ...checkRuns.filter(x => FAILURE.has(x.conclusion)).map(x => ({
      name: typeof x.name === 'string' ? x.name.slice(0, 160) : 'unnamed check',
      conclusion: x.conclusion, source: 'check-run',
    })),
    ...statuses.filter(x => FAILURE.has(x.state)).map(x => ({
      name: typeof x.context === 'string' ? x.context.slice(0, 160) : 'unnamed status',
      conclusion: x.state, source: 'commit-status',
    })),
  ].slice(0, 10);
  // GitHub can return only the first page. Unknown totals must not be called passing.
  const completePage = Number.isSafeInteger(totalCount) && totalCount >= 0 && totalCount === checkRuns.length;
  const status = completePage ? classifyChecks(checkRuns, statuses) : 'incomplete';
  let triage;
  if (status === 'failed') {
    triage = failures.some(x => x.conclusion === 'startup_failure')
      ? 'investigate-runner-admission'
      : failures.some(x => x.conclusion === 'timed_out')
        ? 'investigate-timeout'
        : 'inspect-failing-job-logs';
  } else {
    triage = {
      'no-evidence': 'investigate-workflow-trigger-or-runner',
      pending: 'wait-for-terminal-checks',
      incomplete: 'inspect-missing-or-unrecognized-evidence',
      'passing-observed-only': 'verify-required-gates',
    }[status];
  }
  return { status, triage, failures };
}
