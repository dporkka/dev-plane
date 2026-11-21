#!/usr/bin/env node
"use strict";

// BLACK-BOX QA ONLY. Uses a live local API, real JWT signature validation,
// migrated PostgreSQL, and an unmocked production-built Next.js application.
// No GitHub OAuth flow, production data, or write/merge operations are involved.

const assert = require("node:assert/strict");
const { createHmac, randomUUID } = require("node:crypto");

const A = Object.freeze({
  org: "10000000-0000-4000-8000-000000000001",
  user: "20000000-0000-4000-8000-000000000001",
  project: "30000000-0000-4000-8000-000000000001",
  repo: "40000000-0000-4000-8000-000000000001",
  pr: "80000000-0000-4000-8000-000000000001",
  run: "60000000-0000-4000-8000-000000000001",
  email: "alpha@example.invalid",
  title: "QA Alpha isolated PR",
  finding: "QA Alpha security finding",
});
const B = Object.freeze({
  org: "10000000-0000-4000-8000-000000000002",
  user: "20000000-0000-4000-8000-000000000002",
  project: "30000000-0000-4000-8000-000000000002",
  repo: "40000000-0000-4000-8000-000000000002",
  pr: "80000000-0000-4000-8000-000000000002",
  run: "60000000-0000-4000-8000-000000000002",
  email: "beta@example.invalid",
  title: "QA Beta isolated PR",
  finding: "QA Beta security finding",
});

function loopbackUrl(raw, label) {
  if (!raw) throw new Error(`${label} is required`);
  const url = new URL(raw);
  if (url.protocol !== "http:" ||
      !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname) ||
      url.username || url.password || url.search || url.hash || url.pathname !== "/") {
    throw new Error(`${label} must be an uncredentialed http:// loopback origin`);
  }
  return url.origin;
}

function signedToken(user, secret, { expired = false } = {}) {
  const now = Math.floor(Date.now() / 1000);
  const encode = (value) => Buffer.from(JSON.stringify(value)).toString("base64url");
  const unsigned = [
    encode({ alg: "HS256", typ: "JWT" }),
    encode({
      user_id: user.user, org_id: user.org, email: user.email, role: "admin",
      iss: "dev-plane", aud: "dev-plane-api", iat: now - 5,
      nbf: now - 5, exp: expired ? now - 10 : now + 900, jti: randomUUID(),
    }),
  ].join(".");
  return `${unsigned}.${createHmac("sha256", secret).update(unsigned).digest("base64url")}`;
}

async function request(api, path, token) {
  const result = await fetch(`${api}${path}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    signal: AbortSignal.timeout(8_000),
  });
  let data = null;
  try { data = await result.json(); } catch { /* Some failure bodies are empty. */ }
  return { status: result.status, data };
}

async function testApi(api, tokenA, tokenB, expiredToken) {
  const range = "?start=2026-11-01&end=2030-01-01";
  const authorized = await request(api, "/api/v1/organizations", tokenA);
  assert.equal(authorized.status, 200, "authorized organization listing");
  assert.deepEqual(authorized.data.map((row) => row.id), [A.org]);

  const orgB = await request(api, "/api/v1/organizations", tokenB);
  assert.equal(orgB.status, 200);
  assert.deepEqual(orgB.data.map((row) => row.id), [B.org]);

  assert.equal((await request(api, "/api/v1/organizations")).status, 401);
  assert.equal((await request(api, "/api/v1/organizations", expiredToken)).status, 401);
  assert.equal((await request(api, `/api/v1/projects/${A.project}/pull-requests`, tokenB)).status, 404);
  assert.equal((await request(api, `/api/v1/pull-requests/${A.pr}`, tokenB)).status, 404);
  assert.equal((await request(api, `/api/v1/runs/${A.run}/review`, tokenB)).status, 404);
  assert.equal((await request(api, `/api/v1/organizations/${A.org}/review-metrics${range}`, tokenB)).status, 404);
  assert.equal((await request(api, `/api/v1/pull-requests/${B.pr}`, tokenA)).status, 404);
  assert.equal((await request(api, `/api/v1/runs/${B.run}/review`, tokenA)).status, 404);
  assert.equal((await request(api, `/api/v1/organizations/${B.org}/review-metrics${range}`, tokenA)).status, 404);

  const aPRs = await request(api, `/api/v1/projects/${A.project}/pull-requests`, tokenA);
  assert.equal(aPRs.status, 200);
  assert.deepEqual(aPRs.data.map((row) => row.id), [A.pr]);

  const aReview = await request(api, `/api/v1/runs/${A.run}/review`, tokenA);
  assert.equal(aReview.status, 200);
  assert.match(aReview.data.summary, /QA Alpha persisted review/);

  const aMetrics = await request(api, `/api/v1/organizations/${A.org}/review-metrics${range}`, tokenA);
  assert.equal(aMetrics.status, 200);
  assert.equal(aMetrics.data.reviewed_prs, 1);
  assert.equal(aMetrics.data.critical_findings, 1);
  assert.equal(aMetrics.data.high_findings, 0);
  const filtered = await request(api, `/api/v1/organizations/${A.org}/review-metrics${range}&repository=${B.repo}`, tokenA);
  assert.equal(filtered.status, 200);
  assert.equal(filtered.data.reviewed_prs, 0);

  const bMetrics = await request(api, `/api/v1/organizations/${B.org}/review-metrics${range}`, tokenB);
  assert.equal(bMetrics.status, 200);
  assert.equal(bMetrics.data.reviewed_prs, 1);
  assert.equal(bMetrics.data.critical_findings, 0);
  assert.equal(bMetrics.data.high_findings, 1);
  console.log("PASS: actual API JWT authorization, two-tenant isolation, reports and metrics");
}

async function testBrowser(web, api, tokenA, tokenB) {
  // The dependency is deliberately isolated to this staging harness.
  const { chromium } = require("playwright");
  const browser = await chromium.launch({ headless: true });
  const errors = [];
  const contextA = await browser.newContext({ viewport: { width: 1440, height: 900 }, acceptDownloads: true });
  try {
    await contextA.addInitScript((token) => localStorage.setItem("token", token), tokenA);
    const pageA = await contextA.newPage();
    pageA.on("pageerror", (err) => errors.push(err.message));

    await pageA.goto(`${web}/reviews`, { waitUntil: "domcontentloaded" });
    await pageA.getByRole("heading", { name: "Pull request inbox" }).waitFor();
    await pageA.getByText(A.title).first().waitFor({ timeout: 15_000 });
    assert.equal(await pageA.getByText(B.title).count(), 0, "Beta PR leaked into Alpha inbox");

    await pageA.getByText(A.title).first().click();
    await pageA.getByText(A.finding).first().waitFor({ timeout: 12_000 });
    assert.equal(await pageA.getByText(B.finding).count(), 0, "Beta findings leaked into Alpha details");

    await pageA.goto(`${web}/reviews/analytics`);
    await pageA.getByText("1 linked PRs have persisted review reports.").first().waitFor({ timeout: 12_000 });
    await pageA.getByRole("button", { name: "Review details" }).click();
    await pageA.getByText(A.title).first().waitFor();
    const downloadEvent = pageA.waitForEvent("download");
    await pageA.getByRole("button", { name: "Export data" }).click();
    const download = await downloadEvent;
    assert.match(download.suggestedFilename(), /\.csv$/);
    const csvBytes = await require("node:fs/promises").readFile(await download.path());
    const csv = csvBytes.toString("utf8");
    assert.match(csv, /QA Alpha isolated PR/);
    assert.doesNotMatch(csv, /QA Beta isolated PR/);

    await pageA.setViewportSize({ width: 390, height: 844 });
    await pageA.goto(`${web}/reviews`);
    await pageA.getByRole("heading", { name: "Pull request inbox" }).waitFor();
    await pageA.getByRole("button", { name: "Open navigation" }).click();
    await pageA.getByRole("button", { name: "Close navigation" }).click();
    const mobileOverflow = await pageA.evaluate(() =>
      document.documentElement.scrollWidth > document.documentElement.clientWidth);
    assert.equal(mobileOverflow, false, "mobile page overflows horizontally");

    const contextB = await browser.newContext({ viewport: { width: 1440, height: 900 } });
    try {
      await contextB.addInitScript((token) => localStorage.setItem("token", token), tokenB);
      const pageB = await contextB.newPage();
      pageB.on("pageerror", (err) => errors.push(err.message));
      await pageB.goto(`${web}/reviews`);
      await pageB.getByText(B.title).first().waitFor({ timeout: 15_000 });
      assert.equal(await pageB.getByText(A.title).count(), 0);
      await pageB.goto(`${web}/reviews/${A.pr}`);
      await pageB.getByText("Unable to load this pull request, or access has been denied.").first().waitFor();
      assert.equal(await pageB.getByText(A.finding).count(), 0);
    } finally {
      await contextB.close();
    }
    assert.deepEqual(errors, [], "browser JavaScript errors");
    console.log("PASS: real browser + real API inbox, details, metrics, CSV, mobile, cross-tenant denial");
  } finally {
    await contextA.close();
    await browser.close();
  }
}

async function main() {
  // Validate the origins before doing any network access or constructing tokens.
  const api = loopbackUrl(process.env.STAGING_API_URL, "STAGING_API_URL");
  const web = loopbackUrl(process.env.STAGING_WEB_URL, "STAGING_WEB_URL");
  const secret = process.env.STAGING_TEST_JWT_SECRET;
  if (!secret || secret.length < 32) throw new Error("STAGING_TEST_JWT_SECRET must be provided and at least 32 chars");

  const tokenA = signedToken(A, secret);
  const tokenB = signedToken(B, secret);
  const expired = signedToken(A, secret, { expired: true });
  await testApi(api, tokenA, tokenB, expired);
  await testBrowser(web, api, tokenA, tokenB);
}
if (require.main === module) {
  main().catch((err) => {
    console.error("STAGING SMOKE FAILED:", err.message);
    process.exitCode = 1;
  });
}

module.exports = { loopbackUrl, signedToken };
