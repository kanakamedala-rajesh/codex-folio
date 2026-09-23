/* global fetch, localStorage */
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { URL } from "node:url";
import { chromium } from "playwright";

const [phase, link, profile] = process.argv.slice(2);
assert.ok(phase === "grant" || phase === "attack" || phase === "reopen");
const origin = new URL(link).origin;
const trustedSPKI = process.env.CODEX_FOLIO_TEST_SPKI;
assert.ok(trustedSPKI);
const context = await chromium.launchPersistentContext(profile, {
  executablePath: process.env.CODEX_FOLIO_CHROMIUM || undefined,
  headless: true,
  args: [`--ignore-certificate-errors-spki-list=${trustedSPKI}`],
});
try {
  const page = await context.newPage();
  if (phase === "attack") {
    await assert.rejects(
      page.goto(origin),
      /ERR_CERT_AUTHORITY_INVALID|ERR_CERT_COMMON_NAME_INVALID/,
    );
    console.log("browser PASS: same-port impostor certificate cannot load trusted origin");
  } else if (phase === "grant") {
    await page.goto(origin);
    const token = new URL(link).searchParams.get("bootstrap");
    assert.ok(token);
    const auth = await page.evaluate(async (bootstrapToken) => {
      const response = await fetch("/api/v1/bootstrap", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ bootstrap_token: bootstrapToken }),
      });
      return { status: response.status, body: await response.json() };
    }, token);
    assert.equal(auth.status, 200);
    const granted = await page.evaluate(async (csrf) => {
      const response = await fetch("/api/v1/browser-trust", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-CodexFolio-CSRF": csrf },
        body: JSON.stringify({ action: "grant" }),
      });
      return { status: response.status, body: await response.json() };
    }, auth.body.csrf_token);
    assert.equal(granted.status, 200);
    assert.equal(granted.body.trusted, true);
    assert.ok(granted.body.credential);
    await page.evaluate(
      (credential) => localStorage.setItem("codex-folio.browser-trust.v1", credential),
      granted.body.credential,
    );
    let received;
    const other = createServer((request, response) => {
      received = request.headers;
      response.end("ok");
    });
    await new Promise((resolve) => other.listen(0, "127.0.0.1", resolve));
    try {
      const otherOrigin = `http://127.0.0.1:${other.address().port}`;
      await page.goto(otherOrigin);
      assert.ok(received);
      assert.equal(received["x-codexfolio-trust"], undefined);
      assert.ok(!received.cookie?.includes("codexfolio_trust"));
      assert.equal(
        await page.evaluate(() => localStorage.getItem("codex-folio.browser-trust.v1")),
        null,
      );
    } finally {
      await new Promise((resolve) => other.close(resolve));
    }
    console.log("browser PASS: explicit trust persisted in a real browser profile");
  } else {
    const renewal = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/v1/browser-trust") && response.request().method() === "POST",
    );
    await page.goto(origin);
    const renewed = await renewal;
    assert.equal(renewed.status(), 200);
    const body = await renewed.json();
    assert.equal(body.trusted, true);
    assert.ok(body.csrf_token);
    const metadataStatus = await page.evaluate(async () => (await fetch("/api/v1/meta")).status);
    assert.equal(metadataStatus, 200);
    console.log(
      "browser PASS: clock-expired session renewed after browser and SQLite service restart",
    );
    const forgotten = await page.evaluate(async (csrf) => {
      const credential = localStorage.getItem("codex-folio.browser-trust.v1");
      const response = await fetch("/api/v1/browser-trust", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CodexFolio-CSRF": csrf,
          "X-CodexFolio-Trust": credential || "",
        },
        body: JSON.stringify({ action: "forget" }),
      });
      if (response.ok) localStorage.removeItem("codex-folio.browser-trust.v1");
      const renewal = await fetch("/api/v1/browser-trust", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-CodexFolio-Renew": "1",
          "X-CodexFolio-Trust": credential || "",
        },
        body: JSON.stringify({ action: "renew" }),
      });
      return {
        hadCredential: Boolean(credential),
        forgetStatus: response.status,
        renewalStatus: renewal.status,
        stored: localStorage.getItem("codex-folio.browser-trust.v1"),
      };
    }, body.csrf_token);
    assert.equal(forgotten.hadCredential, true);
    assert.equal(forgotten.forgetStatus, 200);
    assert.equal(forgotten.renewalStatus, 401);
    assert.equal(forgotten.stored, null);
    assert.equal(await page.evaluate(async () => (await fetch("/api/v1/meta")).status), 401);
    console.log("browser PASS: forgetting persisted trust ends access after restart");
  }
} finally {
  await context.close();
}
