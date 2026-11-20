import assert from 'node:assert/strict';
import test from 'node:test';
import { analyzeCheckEvidence, assertCurrentPRHead, classifyChecks } from './pr-ci-evidence.mjs';

const good = { name: 'Tests', status: 'completed', conclusion: 'success', head_sha: 'current-head' };

test('successful observed checks remain advisory, never promotion authorization', () => {
  assert.deepEqual(analyzeCheckEvidence([good], [], { expectedHead: 'current-head', totalCount: 1 }), {
    status: 'passing-observed-only', triage: 'verify-required-gates', failures: [],
  });
});

test('missing checks, incomplete check pagination, and unrecognized conclusions fail closed', () => {
  assert.equal(analyzeCheckEvidence([], [], {expectedHead:'current-head', totalCount:0}).status, 'no-evidence');
  assert.equal(analyzeCheckEvidence([good], [], {expectedHead:'current-head', totalCount:2}).status, 'incomplete');
  assert.equal(analyzeCheckEvidence([good], [], {expectedHead:'current-head', totalCount:undefined}).status, 'incomplete');
  assert.equal(classifyChecks([{status:'completed', conclusion:'neutral'}],[]), 'incomplete');
});

test('reports concrete failing check names without asserting a root cause', () => {
  const e=analyzeCheckEvidence([
    {name:'Build', status:'completed',conclusion:'failure',head_sha:'current-head'},
    {name:'Format',status:'completed',conclusion:'startup_failure',head_sha:'current-head'},
  ], [{context:'security',state:'error'}], {expectedHead:'current-head',totalCount:2});
  assert.equal(e.status,'failed');
  assert.equal(e.triage,'investigate-runner-admission');
  assert.deepEqual(e.failures,[
    {name:'Build',conclusion:'failure',source:'check-run'},
    {name:'Format',conclusion:'startup_failure',source:'check-run'},
    {name:'security',conclusion:'error',source:'commit-status'},
  ]);
});

test('timeout is triaged separately and pending cannot be marked passing', () => {
  assert.equal(analyzeCheckEvidence([{name:'E2E', status:'completed', conclusion:'timed_out'}],[], {expectedHead:'current-head',totalCount:1}).triage,'investigate-timeout');
  assert.equal(analyzeCheckEvidence([{name:'E2E', status:'in_progress', conclusion:null}],[],{expectedHead:'current-head',totalCount:1}).status,'pending');
});

test('rejects check runs for the wrong head and a moved or closed PR', () => {
  assert.throws(() => analyzeCheckEvidence([{...good,head_sha:'old-head'}],[],{expectedHead:'current-head',totalCount:1}), /head mismatch/);
  assert.doesNotThrow(() => assertCurrentPRHead({number:12,state:'open',head:{sha:'current-head'}},'current-head'));
  assert.throws(() => assertCurrentPRHead({number:12,state:'open',head:{sha:'new-head'}},'current-head'),/head changed/);
  assert.throws(() => assertCurrentPRHead({number:12,state:'closed',head:{sha:'current-head'}},'current-head'),/no longer open/);
  assert.throws(() => assertCurrentPRHead(null,'current-head'),/Incomplete PR/);
});

test('malformed check evidence is rejected rather than silently reported green', () => {
  assert.throws(() => analyzeCheckEvidence(null,[],{expectedHead:'current-head',totalCount:0}), /arrays/);
  assert.throws(() => analyzeCheckEvidence([null],[],{expectedHead:'current-head',totalCount:1}), /Malformed/);
  assert.throws(() => analyzeCheckEvidence([good],null,{expectedHead:'current-head',totalCount:1}), /arrays/);
});
