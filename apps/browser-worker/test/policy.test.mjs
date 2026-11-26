import test from 'node:test';
import assert from 'node:assert/strict';
import { validateJob, isAllowedRequestURL } from '../src/policy.mjs';

const valid = () => ({
  version: 1,
  tenant_id: 'tenant-a',
  run_id: 'run-001',
  start_url: 'https://example.com/dashboard',
  checks: [{ type: 'text_visible', value: 'Dashboard' }],
  timeout_ms: 5000,
});
const config = { allowedOrigins: ['https://example.com'] };

test('accepts a bounded read-only verification job on an operator allowlisted origin', () => {
  const result = validateJob(valid(), config);
  assert.equal(result.start_url, 'https://example.com/dashboard');
  assert.equal(result.capture.screenshot, false);
});

test('rejects missing operator allowlist and origins supplied only by the job', () => {
  assert.throws(() => validateJob(valid(), { allowedOrigins: [] }), /operator allowlist/i);
  assert.throws(() => validateJob({ ...valid(), start_url: 'https://elsewhere.com' }, config), /not operator allowlisted/i);
});

test('rejects untrusted schemes, userinfo, and HTTP except explicitly enabled loopback', () => {
  for (const url of ['file:///etc/passwd', 'javascript:alert(1)', 'https://user:password@example.com/', 'http://example.com/']) {
    assert.throws(() => validateJob({ ...valid(), start_url: url }, { allowedOrigins: [new URL(url).origin] }), /scheme|credentials/i);
  }
  const local = { ...valid(), start_url: 'http://127.0.0.1:3000/' };
  assert.throws(() => validateJob(local, { allowedOrigins: ['http://127.0.0.1:3000'] }), /HTTP/i);
  assert.equal(validateJob(local, { allowedOrigins: ['http://127.0.0.1:3000'], allowHttpLoopback: true }).start_url, local.start_url);
  assert.throws(() => validateJob({ ...valid(), start_url: 'http://192.168.1.2/' }, { allowedOrigins: ['http://192.168.1.2'], allowHttpLoopback: true }), /HTTP/i);
});

test('rejects effectful actions, oversized specifications, and unsafe artifact identifiers', () => {
  assert.throws(() => validateJob({ ...valid(), checks: [{ type: 'click', value: '#pay' }] }, config), /unsupported check/i);
  assert.throws(() => validateJob({ ...valid(), checks: Array.from({ length: 21 }, () => ({ type: 'selector_visible', value: '#ready' })) }, config), /checks/i);
  assert.throws(() => validateJob({ ...valid(), run_id: '../escape' }, config), /run_id/i);
  assert.throws(() => validateJob({ ...valid(), timeout_ms: 99999999 }, config), /timeout_ms/i);
});

test('request policy blocks other origins, alternate schemes, and credential URLs', () => {
  assert.equal(isAllowedRequestURL('https://example.com/assets/main.js', 'https://example.com'), true);
  assert.equal(isAllowedRequestURL('https://evil.example.com/', 'https://example.com'), false);
  assert.equal(isAllowedRequestURL('https://example.com:444/', 'https://example.com'), false);
  assert.equal(isAllowedRequestURL('file:///etc/hosts', 'https://example.com'), false);
  assert.equal(isAllowedRequestURL('https://x:y@example.com/', 'https://example.com'), false);
});
