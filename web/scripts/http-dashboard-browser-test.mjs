/* global localStorage, sessionStorage, fetch */
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { URL, URLSearchParams } from "node:url";
import { chromium } from "playwright";

const [link, freshLink, output] = process.argv.slice(2);
const origin = new URL(link).origin;
assert.equal(new URL(link).protocol, "http:");
assert.equal(new URL(freshLink).origin, origin);
assert.ok(new URL(link).hash.startsWith("#bootstrap="));
const checks = [];
const check = (name) => {
  checks.push(name);
  console.log(`browser PASS: ${name}`);
};
// This suite intentionally uses ordinary Chromium certificate settings.
const browser = await chromium.launch({
  executablePath: process.env.CODEX_FOLIO_CHROMIUM || undefined,
  headless: true,
});
try {
  const context = await browser.newContext();
  await context.addInitScript(() => {
    localStorage.setItem("codex-folio.browser-trust.v1", "discarded-fixture-trust");
  });
  const page = await context.newPage();
  page.setDefaultTimeout(30_000);
  let authenticatedRead = false;
  let leakedCredential = false;
  page.on("request", (request) => {
    const headers = request.headers();
    if (headers["x-codexfolio-trust"] || headers.cookie) leakedCredential = true;
    if (new URL(request.url()).pathname === "/api/v1/profiles" && headers["x-codexfolio-session"])
      authenticatedRead = true;
  });
  const loaded = async () => {
    await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
    assert.equal(
      await page.getByRole("heading", { name: "Trusted browser", exact: true }).count(),
      0,
    );
    assert.equal(new URL(page.url()).hash, "");
    assert.equal(new URL(page.url()).search, "");
    assert.equal(
      await page.evaluate(() => localStorage.getItem("codex-folio.browser-trust.v1")),
      null,
    );
    assert.equal((await context.cookies()).length, 0);
    assert.ok(!leakedCredential, "browser sent cookie or reusable trust credential");
  };
  await page.goto(link);
  await loaded();
  assert.ok(authenticatedRead, "profile read must use explicit session header");
  check("HTTP dashboard opens without certificate override, removes fragment, and loads profiles");
  check("HTTP session uses explicit header with no cookies or reusable browser trust");
  await page.reload();
  await loaded();
  check("same-tab refresh retains the live session");

  const replayStatus = await page.evaluate(
    async (token) => {
      const response = await fetch("/api/v1/bootstrap", {
        method: "POST",
        credentials: "omit",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ bootstrap_token: token }),
      });
      return response.status;
    },
    new URLSearchParams(new URL(link).hash.slice(1)).get("bootstrap"),
  );
  assert.ok([401, 403].includes(replayStatus), "used bootstrap must not be replayable");
  check("one-time bootstrap rejects replay");

  let otherRequestSafe = false;
  const other = createServer((request, response) => {
    otherRequestSafe =
      !request.headers.cookie &&
      !request.headers["x-codexfolio-session"] &&
      !request.headers["x-codexfolio-trust"];
    response.setHeader("Content-Type", "text/html");
    response.end("<!doctype html><title>Other loopback origin</title>");
  });
  await new Promise((resolve) => other.listen(0, "127.0.0.1", resolve));
  try {
    const otherPage = await context.newPage();
    await otherPage.goto(`http://127.0.0.1:${other.address().port}/`);
    assert.ok(otherRequestSafe, "another port must not receive session credentials");
    assert.equal(
      await otherPage.evaluate(() => sessionStorage.getItem("codex-folio.browser-session.v1")),
      null,
    );
    await otherPage.close();
  } finally {
    await new Promise((resolve) => {
      other.close(resolve);
      other.closeAllConnections();
    });
  }
  check("another loopback port receives no session header, cookie, or stored session");

  const privateContext = await browser.newContext();
  const privatePage = await privateContext.newPage();
  await privatePage.goto(origin);
  await privatePage.getByRole("heading", { name: "Relaunch CodexFolio", exact: true }).waitFor();
  const unauthenticatedStatus = await privatePage.evaluate(
    async () => (await fetch("/api/v1/profiles")).status,
  );
  assert.ok([401, 403].includes(unauthenticatedStatus));
  check("new browser context cannot authorize from plain dashboard address");
  await privateContext.close();

  await page.goto(freshLink);
  await loaded();
  check("fresh one-time URL authorizes a replacement session");
  mkdirSync(output, { recursive: true });
  await page.screenshot({ path: join(output, "http-dashboard.png"), fullPage: true });
  writeFileSync(
    join(output, "http-dashboard.json"),
    JSON.stringify({ suite: "http-dashboard", browser: browser.version(), checks }, null, 2),
  );
} catch {
  // Playwright errors may contain the bootstrap URL; keep fixture credentials out of logs.
  console.error(`HTTP dashboard regression failed after ${checks.length} completed checks`);
  process.exitCode = 1;
} finally {
  await browser.close();
}
