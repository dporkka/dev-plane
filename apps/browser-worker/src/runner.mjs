import { createHash } from 'node:crypto';
import { lstat, mkdir, readFile, rename, rm, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { validateJob, isAllowedRequestURL } from './policy.mjs';

async function atomicJSON(path, value) {
  const tmp = `${path}.${process.pid}.tmp`;
  try {
    await writeFile(tmp, JSON.stringify(value, null, 2) + '\n', { mode: 0o600, flag: 'wx' });
    await rename(tmp, path);
  } finally {
    await rm(tmp, { force: true });
  }
}

// Caller controls the browser executable and operator allowlist, never the job document.
export async function runJob(input, config = {}) {
  const job = validateJob(input, config);
  if (typeof config.artifactRoot !== 'string' || config.artifactRoot.length === 0) throw new Error('artifactRoot is required');
  const root = resolve(config.artifactRoot);
  const tenantDir = join(root, job.tenant_id);
  const outputDir = join(tenantDir, job.run_id);
  await mkdir(outputDir, { recursive: true, mode: 0o700 });
  for (const path of [tenantDir, outputDir]) {
    if ((await lstat(path)).isSymbolicLink()) throw new Error('artifact directories must not be symlinks');
  }
  const digest = createHash('sha256').update(JSON.stringify(job)).digest('hex');
  const resultPath = join(outputDir, 'result.json');
  try {
    const existing = JSON.parse(await readFile(resultPath, 'utf8'));
    if (existing.spec_digest !== digest) throw new Error('run ID reused with conflicting job specification');
    return existing;
  } catch (error) {
    if (error.code !== 'ENOENT') {
      if (error instanceof SyntaxError) throw new Error('existing result is corrupt; manual reconciliation required');
      throw error;
    }
  }

  // Crash after this claim requires reconciliation before a retry, never a silent replay.
  const claimPath = join(outputDir, 'claim.json');
  try {
    await writeFile(claimPath, JSON.stringify({ spec_digest: digest, claimed_at: new Date().toISOString() }), { flag: 'wx', mode: 0o600 });
  } catch (error) {
    if (error.code === 'EEXIST') throw new Error('run is already claimed or interrupted; manual reconciliation required');
    throw error;
  }

  const started = Date.now();
  const deadline = started + job.timeout_ms;
  const result = {
    version: 1, tenant_id: job.tenant_id, run_id: job.run_id,
    spec_digest: digest, status: 'failed', checks_verified: 0, artifacts: {}, duration_ms: 0,
  };
  let browser;
  let context;
  let page;
  let tracing = false;
  try {
    // Lazy import keeps policy tests independent of browser dependencies.
    const { chromium } = config.chromium ? { chromium: config.chromium } : await import('playwright-core');
    browser = await chromium.launch({ headless: true, ...(config.executablePath ? { executablePath: config.executablePath } : {}), timeout: job.timeout_ms });
    context = await browser.newContext({ serviceWorkers: 'block', acceptDownloads: false, ignoreHTTPSErrors: false });
    const origin = new URL(job.start_url).origin;
    await context.route('**/*', route => {
      const safeMethod = ['GET', 'HEAD'].includes(route.request().method());
      return safeMethod && isAllowedRequestURL(route.request().url(), origin)
        ? route.continue() : route.abort('blockedbyclient');
    });
    // WebSockets can transmit mutations even when all HTTP requests are GET-only.
    await context.routeWebSocket('**/*', route => route.close());
    if (job.capture.trace) {
      await context.tracing.start({ screenshots: true, snapshots: true });
      tracing = true;
    }
    page = await context.newPage();
    page.on('popup', popup => { void popup.close(); });
    const remaining = () => {
      const ms = deadline - Date.now();
      if (ms <= 0) throw new Error('deadline exceeded');
      return ms;
    };
    await page.goto(job.start_url, { waitUntil: 'domcontentloaded', timeout: remaining() });
    if (!isAllowedRequestURL(page.url(), origin)) throw new Error('navigation escaped operator allowlist');
    for (let i = 0; i < job.checks.length; i++) {
      const check = job.checks[i];
      try {
        const target = check.type === 'text_visible'
          ? page.getByText(check.value, { exact: true }).first()
          : page.locator(check.value).first();
        await target.waitFor({ state: 'visible', timeout: remaining() });
        if (!isAllowedRequestURL(page.url(), origin)) throw new Error('navigation escaped operator allowlist');
        result.checks_verified++;
      } catch {
        throw new Error(`verification check ${i + 1} failed`);
      }
    }
    result.status = 'passed';
  } catch (error) {
    // Do not persist Playwright exceptions: they may contain URLs, query secrets, or DOM excerpts.
    result.failure = error.message?.startsWith('verification check ') ? error.message : 'browser execution failed';
  } finally {
    if (page && job.capture.screenshot) {
      try {
        await page.screenshot({ path: join(outputDir, 'screenshot.png'), timeout: 3000 });
        result.artifacts.screenshot = 'screenshot.png';
      } catch { /* evidence is best effort; do not claim it exists */ }
    }
    if (context && tracing) {
      try {
        await context.tracing.stop({ path: join(outputDir, 'trace.zip') });
        result.artifacts.trace = 'trace.zip';
      } catch { /* no trace claim when writing fails */ }
    }
    try { await browser?.close(); } catch { /* preserve verification result */ }
    if ((job.capture.screenshot && !result.artifacts.screenshot) || (job.capture.trace && !result.artifacts.trace)) {
      result.status = 'failed';
      result.failure = 'required verification evidence could not be captured';
    }
    result.duration_ms = Date.now() - started;
    await atomicJSON(resultPath, result);
    await rm(claimPath, { force: true });
  }
  return result;
}
