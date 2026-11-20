#!/usr/bin/env node
import { readFile } from 'node:fs/promises';
import { runJob } from './runner.mjs';

async function main() {
  if (process.argv.length !== 3) throw new Error('usage: node src/cli.mjs job.json');
  const job = JSON.parse(await readFile(process.argv[2], 'utf8'));
  const result = await runJob(job, {
    allowedOrigins: (process.env.BROWSER_ALLOWED_ORIGINS || '').split(',').map(s => s.trim()).filter(Boolean),
    allowHttpLoopback: process.env.BROWSER_ALLOW_HTTP_LOOPBACK === '1',
    artifactRoot: process.env.BROWSER_ARTIFACT_ROOT,
    executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE || undefined,
  });
  process.stdout.write(JSON.stringify(result) + '\n');
  if (result.status !== 'passed') process.exitCode = 1;
}

main().catch(error => {
  // Only print stable, bounded validation messages; never print raw browser/DOM errors.
  const safe = /^(usage:|operator allowlist|start_url|invalid |only HTTPS|unsupported |checks |timeout_ms|capture |artifactRoot|run is already claimed|run ID reused|existing result is corrupt)/.test(error.message);
  process.stderr.write((safe ? error.message : 'browser run could not be started') + '\n');
  process.exitCode = 2;
});
