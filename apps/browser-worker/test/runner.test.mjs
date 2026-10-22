import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createServer } from 'node:http';
import { runJob } from '../src/runner.mjs';

async function fixture(t) {
  const server = createServer((_req, res) => {
    res.writeHead(200, { 'Content-Type': 'text/html' });
    res.end('<!doctype html><title>Browser probe</title><h1 id="ready">Ready</h1>');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${server.address().port}/`;
  const dir = await mkdtemp(join(tmpdir(), 'devplane-browser-test-'));
  t.after(async () => { await new Promise(resolve => server.close(resolve)); await rm(dir, { recursive: true, force: true }); });
  return { url, dir };
}

const chrome = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE;
const jobFor = (url, id) => ({
  version: 1,
  tenant_id: 'integration',
  run_id: id,
  start_url: url,
  checks: [{ type: 'text_visible', value: 'Ready' }, { type: 'selector_visible', value: '#ready' }],
  timeout_ms: 30000,
  capture: { screenshot: true, trace: true },
});

test('real Chromium run independently verifies DOM and persists screenshot, trace and result', { skip: !chrome }, async t => {
  const { url, dir } = await fixture(t);
  const result = await runJob(jobFor(url, 'verified-1'), {
    artifactRoot: dir, allowedOrigins: [new URL(url).origin], allowHttpLoopback: true, executablePath: chrome,
  });
  assert.equal(result.status, 'passed', JSON.stringify(result));
  assert.equal(result.checks_verified, 2);
  assert.equal(result.artifacts.screenshot, 'screenshot.png');
  assert.equal(result.artifacts.trace, 'trace.zip');
  const output = join(dir, 'integration', 'verified-1');
  assert.ok((await readFile(join(output, 'screenshot.png'))).length > 100);
  assert.ok((await readFile(join(output, 'trace.zip'))).length > 100);
  assert.equal(JSON.parse(await readFile(join(output, 'result.json'), 'utf8')).status, 'passed');
  const replay = await runJob(jobFor(url, 'verified-1'), {
    artifactRoot: dir, allowedOrigins: [new URL(url).origin], allowHttpLoopback: true,
    chromium: { launch() { throw new Error('must not launch again'); } },
  });
  assert.deepEqual(replay, result);
  await assert.rejects(() => runJob({ ...jobFor(url, 'verified-1'), checks: [{ type: 'text_visible', value: 'Changed' }] }, {
    artifactRoot: dir, allowedOrigins: [new URL(url).origin], allowHttpLoopback: true,
  }), /conflicting job specification/i);
});

test('failed independent assertion persists fail-closed evidence', { skip: !chrome }, async t => {
  const { url, dir } = await fixture(t);
  const job = { ...jobFor(url, 'failed-1'), checks: [{ type: 'text_visible', value: 'Not present' }], timeout_ms: 2500 };
  const result = await runJob(job, {
    artifactRoot: dir, allowedOrigins: [new URL(url).origin], allowHttpLoopback: true, executablePath: chrome,
  });
  assert.equal(result.status, 'failed');
  assert.equal(result.checks_verified, 0);
  assert.match(result.failure, /verification check 1 failed/i);
  assert.equal(JSON.parse(await readFile(join(dir, 'integration', 'failed-1', 'result.json'), 'utf8')).status, 'failed');
});

test('existing claim never automatically replays a potentially interrupted run', async t => {
  const { url, dir } = await fixture(t);
  const claimDir = join(dir, 'integration', 'interrupted-1');
  const { mkdir, writeFile } = await import('node:fs/promises');
  await mkdir(claimDir, { recursive: true });
  await writeFile(join(claimDir, 'claim.json'), '{}');
  await assert.rejects(() => runJob(jobFor(url, 'interrupted-1'), {
    artifactRoot: dir, allowedOrigins: [new URL(url).origin], allowHttpLoopback: true,
  }), /interrupted.*reconciliation/i);
});

test('read-only browser prevents a loaded page from sending a POST effect', { skip: !chrome }, async t => {
  let postCount = 0;
  const server = createServer((req, res) => {
    if (req.method === 'POST') { postCount++; res.end('posted'); return; }
    res.writeHead(200, { 'Content-Type': 'text/html' });
    res.end('<h1>Ready</h1><script>fetch("/",{method:"POST",body:"danger"}).catch(()=>{})</script>');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const dir = await mkdtemp(join(tmpdir(), 'devplane-no-write-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const url = `http://127.0.0.1:${server.address().port}/`;
  const result = await runJob({ ...jobFor(url, 'no-write-1'), checks: [{ type: 'text_visible', value: 'Ready' }], capture: { screenshot: false, trace: false } }, {
    artifactRoot: dir, allowedOrigins: [new URL(url).origin], allowHttpLoopback: true, executablePath: chrome,
  });
  assert.equal(result.status, 'passed');
  assert.equal(postCount, 0);
});

test('artifact tenant path cannot be redirected through a symlink', async t => {
  const { url, dir } = await fixture(t);
  const { symlink } = await import('node:fs/promises');
  const other = await mkdtemp(join(tmpdir(), 'devplane-other-tenant-'));
  t.after(() => rm(other, { recursive: true, force: true }));
  await symlink(other, join(dir, 'integration'));
  await assert.rejects(() => runJob(jobFor(url, 'symlink-1'), {
    artifactRoot: dir, allowedOrigins: [new URL(url).origin], allowHttpLoopback: true,
  }), /artifact directories must not be symlinks/i);
});
