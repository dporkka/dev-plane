// Run after building the Next.js web app with
// NEXT_PUBLIC_API_URL=http://127.0.0.1:56521.
// The fake API only simulates cookie transport; it never calls GitHub or uses credentials.
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const webOrigin = "http://127.0.0.1:56522";
const apiOrigin = "http://127.0.0.1:56521";
let callbackCalls = 0;

const backend = createServer((req, res) => {
  const url = new URL(req.url || "/", apiOrigin);
  if (process.env.OAUTH_BRIDGE_DEBUG === "1") console.log("BACKEND PATH", url.pathname);
  if (url.pathname === "/api/v1/auth/github") {
    res.writeHead(307, {
      Location: "https://github.com/login/oauth/authorize?client_id=qa-client&state=qa-state",
      "Set-Cookie": "oauth_state=qa-state; Path=/; Max-Age=600; HttpOnly; SameSite=Lax",
    });
    res.end();
    return;
  }
  if (url.pathname === "/api/v1/auth/github/callback") {
    callbackCalls++;
    if (req.headers.cookie !== "oauth_state=qa-state" ||
        url.searchParams.get("state") !== "qa-state" ||
        url.searchParams.get("code") !== "qa-code") {
      res.writeHead(400).end("Invalid callback");
      return;
    }
    res.writeHead(200, {
      "Content-Type": "application/json",
      "Set-Cookie": "oauth_state=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax",
    });
    res.end(JSON.stringify({ token:"qa.header.signature" }));
    return;
  }
  res.writeHead(404).end();
});
let web;

async function waitForWeb() {
  for (let i = 0; i < 50; i++) {
    try {
      await fetch(`${webOrigin}/favicon.ico`, { signal:AbortSignal.timeout(500) });
      return;
    } catch {
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
  }
  throw new Error("Next.js server never became ready");
}

async function main() {
  await new Promise((resolve, reject) => backend.listen(56521, "127.0.0.1", (err) =>
    err ? reject(err) : resolve()));
  web = spawn(process.execPath, ["node_modules/next/dist/bin/next", "start", "--hostname",
    "127.0.0.1", "--port", "56522"], {
    cwd: resolve(root, "apps/web"),
    env: { ...process.env, NEXT_PUBLIC_API_URL:apiOrigin, NEXT_TELEMETRY_DISABLED:"1" },
    stdio:["ignore", "pipe", "pipe"],
  });
  let webErrors = "";
  web.stderr.on("data", (data) => { webErrors += data.toString(); });
  await waitForWeb();
  const begin = await fetch(`${webOrigin}/api/auth/github/callback`, { redirect:"manual" });
  assert.equal(begin.status, 307, "web should redirect directly to GitHub, not to the API host");
  assert.equal(new URL(begin.headers.get("location") || "").hostname, "github.com");
  assert.match(begin.headers.get("set-cookie") || "", /oauth_state=qa-state/);
  assert.match(begin.headers.get("cache-control") || "", /no-store/);

  const bad = await fetch(`${webOrigin}/api/auth/github/callback?code=qa-code&state=wrong`, {
    headers: { Cookie:"oauth_state=qa-state" },
    redirect:"manual",
  });
  assert.equal(bad.status, 400);
  assert.equal(callbackCalls, 0, "mismatched state should be rejected by the web route");

  const callback = await fetch(`${webOrigin}/api/auth/github/callback?code=qa-code&state=qa-state`, {
    headers: { Cookie:"oauth_state=qa-state" },
    redirect:"manual",
  });
  assert.equal(callback.status, 200);
  assert.match(await callback.text(), /localStorage\.setItem\('token', "qa\.header\.signature"\)/);
  assert.match(callback.headers.get("cache-control") || "", /no-store/);
  assert.match(callback.headers.get("set-cookie") || "", /oauth_state=;/);
  assert.equal(callbackCalls, 1);

  const unsafe = await fetch(`${webOrigin}/api/auth/github/callback?token=injected.jwt.token`, {
    redirect:"manual",
  });
  assert.equal(unsafe.status,400, "bearer tokens must not be accepted from URL query parameters");

  console.log("PASS: web-origin OAuth state, callback forwarding/clearing and unsafe-token rejection");
}

main().catch((error) => {
  console.error("OAUTH BRIDGE TEST FAILED:", error.message);
  process.exitCode = 1;
}).finally(async () => {
  if (web) {
    web.kill("SIGTERM");
    try { await Promise.race([once(web, "exit"), new Promise((r)=>setTimeout(r,1200))]); } catch {}
  }
  backend.closeAllConnections();
  await new Promise((resolve) => backend.close(resolve));
});
