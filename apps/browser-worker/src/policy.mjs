// Browser jobs are read-only verification contracts. Never accept effectful actions here.
const IDENTIFIER = /^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/;
const CHECK_TYPES = new Set(['text_visible', 'selector_visible']);
const JOB_KEYS = new Set(['version', 'tenant_id', 'run_id', 'start_url', 'checks', 'timeout_ms', 'capture']);

function plainObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function validateURL(value, allowHttpLoopback) {
  if (typeof value !== 'string' || value.length > 2048) throw new Error('invalid start_url');
  let url;
  try { url = new URL(value); } catch { throw new Error('invalid start_url'); }
  if (url.username || url.password) throw new Error('URL credentials are not permitted');
  if (url.protocol !== 'https:') {
    const local = url.protocol === 'http:' && ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname);
    if (!local || !allowHttpLoopback) throw new Error('only HTTPS scheme is permitted (HTTP loopback requires operator opt-in)');
  }
  return url;
}

export function validateJob(job, config = {}) {
  if (!plainObject(job)) throw new Error('job must be an object');
  if (Object.keys(job).some(key => !JOB_KEYS.has(key))) throw new Error('unsupported job field');
  if (job.version !== 1) throw new Error('unsupported browser job version');
  for (const key of ['tenant_id', 'run_id']) {
    if (!IDENTIFIER.test(job[key] ?? '')) throw new Error(`invalid ${key}`);
  }
  if (!Array.isArray(config.allowedOrigins) || config.allowedOrigins.length === 0) throw new Error('operator allowlist is required');
  const start = validateURL(job.start_url, config.allowHttpLoopback === true);
  const origins = config.allowedOrigins.map(origin => {
    const parsed = validateURL(origin, config.allowHttpLoopback === true);
    if (parsed.href !== `${parsed.origin}/`) throw new Error('operator allowlist must contain origins only');
    return parsed.origin;
  });
  if (!origins.includes(start.origin)) throw new Error('start_url is not operator allowlisted');
  if (!Array.isArray(job.checks) || job.checks.length === 0 || job.checks.length > 20) throw new Error('checks must contain 1 to 20 assertions');
  const checks = job.checks.map(check => {
    if (!plainObject(check) || !CHECK_TYPES.has(check.type)) throw new Error('unsupported check (read-only assertions only)');
    if (Object.keys(check).some(key => key !== 'type' && key !== 'value')) throw new Error('unsupported check field');
    if (typeof check.value !== 'string' || check.value.length === 0 || check.value.length > 500) throw new Error('invalid check value');
    return { type: check.type, value: check.value };
  });
  if (!Number.isInteger(job.timeout_ms) || job.timeout_ms < 1000 || job.timeout_ms > 120000) throw new Error('timeout_ms must be between 1000 and 120000');
  if (job.capture !== undefined && (!plainObject(job.capture) || Object.keys(job.capture).some(key => key !== 'screenshot' && key !== 'trace'))) throw new Error('invalid capture options');
  const capture = {
    screenshot: job.capture?.screenshot ?? false,
    trace: job.capture?.trace ?? false,
  };
  if (typeof capture.screenshot !== 'boolean' || typeof capture.trace !== 'boolean') throw new Error('capture values must be booleans');
  return {
    version: 1,
    tenant_id: job.tenant_id,
    run_id: job.run_id,
    start_url: start.href,
    checks,
    timeout_ms: job.timeout_ms,
    capture,
  };
}

export function isAllowedRequestURL(value, allowedOrigin) {
  try {
    const url = new URL(value);
    return !url.username && !url.password &&
      (url.protocol === 'http:' || url.protocol === 'https:') &&
      url.origin === allowedOrigin;
  } catch { return false; }
}
