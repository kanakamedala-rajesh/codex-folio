/* global document, window, innerWidth, getComputedStyle */
import { URL } from "node:url";
import { performance } from "node:perf_hooks";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { cpus, release, totalmem, tmpdir } from "node:os";
import { chromium } from "playwright";
import axe from "axe-core";

const [link, control, phase] = process.argv.slice(2);
const output =
  process.env.CODEX_FOLIO_BROWSER_OUTPUT ?? join(tmpdir(), "codex-folio-overview-browser");
mkdirSync(output, { recursive: true });
const browser = await chromium.launch({
  executablePath: process.env.CODEX_FOLIO_CHROMIUM || undefined,
  headless: true,
});
const context = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  colorScheme: "dark",
});
const page = await context.newPage();
page.setDefaultTimeout(10000);
const errors = [],
  remote = [];
page.on("pageerror", (error) => errors.push(error.message));
page.on("request", (request) => {
  if (new URL(request.url()).origin !== new URL(link).origin) remote.push(request.url());
});
const results = {
  browser: browser.version(),
  platform: process.platform,
  architecture: process.arch,
  kernel: release(),
  hardware: { cpu: cpus()[0]?.model, logicalCPUs: cpus().length, memoryBytes: totalmem() },
  viewports: [],
  checks: [],
  limitations: [
    "Fake Codex, not live provider",
    "Headless browser accessibility tree and axe are not spoken screen-reader evidence",
    "Native OS contrast and Firefox/Safari unqualified",
  ],
};
const check = (name) => {
  results.checks.push(name);
  console.log(`browser PASS: ${name}`);
};
const scope = () =>
  page.getByRole("combobox", { name: "Dashboard Scope", exact: true }).filter({ visible: true });
async function choose(value) {
  const changed =
    value === "*"
      ? null
      : page.waitForResponse(
          (response) =>
            response.url().endsWith("/api/v1/selection") && response.request().method() === "PUT",
        );
  await scope().selectOption(value);
  if (changed) await changed;
  await page.waitForFunction(
    (expected) =>
      [...document.querySelectorAll('select[aria-label="Dashboard Scope"]')].every(
        (select) => select.value === expected && !select.disabled,
      ),
    value,
  );
  if (changed)
    await page
      .getByText("Selected Profile updated for future interactive launches.", { exact: true })
      .waitFor();
}
async function scenario(mode) {
  writeFileSync(control, mode);
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.getByRole("button", { name: "Refresh", exact: true }).waitFor();
  await page.waitForFunction(() => !document.querySelector("select")?.disabled);
}
async function capture(name, width, height) {
  await page.setViewportSize({ width, height });
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: join(output, `${name}-viewport.png`), fullPage: false });
  const navigation = page.getByRole("navigation", { name: "Primary narrow", exact: true });
  if (width < 700) await navigation.evaluate((nav) => nav.classList.replace("fixed", "static"));
  await page.screenshot({ path: join(output, `${name}.png`), fullPage: true });
  if (width < 700) await navigation.evaluate((nav) => nav.classList.replace("static", "fixed"));
  assert.equal(
    await page.evaluate(() => document.documentElement.scrollWidth > innerWidth),
    false,
    `${name}: horizontal overflow`,
  );
  results.viewports.push({ name, width, height });
}
try {
  if (phase === "reentry") {
    await page.goto(link);
    await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
    assert.ok(!page.url().includes("bootstrap="));
    await page.waitForFunction(() => !document.querySelector("select")?.disabled);
    await context.setOffline(true);
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await page
      .getByText(
        "The local service is unavailable. Start it again in your terminal and open the new dashboard link.",
        { exact: true },
      )
      .waitFor();
    await capture("service-unavailable", 1440, 1000);
    check(
      "fresh reused-service entry succeeds; unavailable loopback connection has terminal guidance",
    );
  } else {
    // Fresh browser has no authority, even though it can load the offline shell.
    const noAuth = await page.request.get(new URL("/api/v1/analytics", link).href);
    assert.equal(noAuth.status(), 401);
    const start = performance.now();
    await page.goto(link);
    await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
    results.cachedEvidenceMs = performance.now() - start;
    assert.ok(
      results.cachedEvidenceMs < 1000,
      `cached evidence ${results.cachedEvidenceMs.toFixed(1)}ms exceeds 1s engineering budget`,
    );
    assert.ok(!page.url().includes("bootstrap="));
    await page
      .getByText("Refresh failed. Last-known values keep their original capture times.", {
        exact: true,
      })
      .waitFor();
    assert.match(await page.locator("main").innerText(), /18 minutes ago/);
    assert.equal(await page.getByText("Recommended", { exact: true }).count(), 0);
    check(
      "one-time bootstrap stripped; stale cached evidence under one second; failed open refresh retains ages",
    );
    assert.equal(await page.title(), "VenkataSudha CodexFolio");
    assert.equal(await page.locator("vite-error-overlay").count(), 0);
    const noCSRF = await page.request.put(new URL("/api/v1/selection", link).href, {
      data: { alias: "Personal" },
      headers: { Origin: new URL(link).origin },
    });
    assert.equal(noCSRF.status(), 403);
    await capture("stale-wide", 1440, 1000);
    await choose("Personal");
    await page
      .getByText("Selected Profile updated for future interactive launches.", { exact: true })
      .waitFor();
    await page
      .getByRole("heading", { name: "Your running Launch Profile is unchanged", exact: true })
      .waitFor();
    await choose("*");
    assert.match(await page.locator("main").innerText(), /Selected Profile: Personal/);
    assert.equal(
      await page.getByRole("heading", { name: "Recent capacity", exact: false }).count(),
      2,
    );
    await choose("Personal");
    check("single profile persists; explicit combined scope preserves Selected Profile");
    await scenario("supported");
    await page.getByText("Capacity refreshed.", { exact: true }).waitFor();
    assert.match(await page.locator("main").innerText(), /75%/);
    assert.match(await page.locator("main").innerText(), /60%/);
    await choose("Work");
    await page.getByText("Recommended", { exact: true }).waitFor();
    await choose("Personal");
    const table = page.getByRole("table");
    assert.match(await table.innerText(), /75%/);
    assert.match(await table.innerText(), /60%/);
    await page.getByRole("slider", { name: "Inspect capture" }).focus();
    await page.keyboard.press("End");
    assert.match(await page.locator("tr[data-current]").innerText(), /75%/);
    check("provider capacity and raw trace/table values agree; keyboard sample inspection");
    await page.getByRole("button", { name: "Launch Codex", exact: true }).click();
    assert.match(await page.locator("main").innerText(), /codex-folio launch Personal/);
    assert.match(await page.locator("main").innerText(), /nothing has been launched/);
    await page.getByRole("button", { name: "Close", exact: true }).click();
    await page.getByRole("button", { name: "Prepare Handoff", exact: true }).click();
    assert.match(await page.locator("main").innerText(), /Nothing has been prepared or started/);
    await page.getByRole("button", { name: "Close", exact: true }).click();
    await page.getByRole("button", { name: "Open details", exact: true }).click();
    assert.match(await page.locator("main").innerText(), /codex.primary.used_percent/);
    await page.getByRole("button", { name: "Open details", exact: true }).click();
    check("foreground terminal guidance and progressive evidence disclosure");
    await capture("running-wide", 1440, 1000);
    await choose("Work");
    await capture("overview-wide", 1440, 1000);
    assert.notEqual(
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Overview", exact: true })
        .evaluate((button) => getComputedStyle(button).color),
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Profiles", exact: true })
        .evaluate((button) => getComputedStyle(button).color),
    );
    await capture("overview-medium", 900, 1000);
    assert.equal(await scope().count(), 1);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.locator('td[colspan="3"] summary').first().click();
    assert.match(
      await page.locator('td[colspan="3"]').first().innerText(),
      /Primary window[\s\S]*65%[\s\S]*Secondary window[\s\S]*50%/,
    );
    await capture("overview-narrow", 390, 844);
    await page.getByText("More", { exact: true }).click();
    await page
      .getByRole("button", { name: "Settings", exact: true })
      .filter({ visible: true })
      .click();
    await page.waitForFunction(() => document.activeElement?.tagName === "H1");
    assert.equal(
      await page
        .getByRole("navigation", { name: "Primary narrow", exact: true })
        .locator("details")
        .getAttribute("open"),
      null,
    );
    await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("light");
    assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "light");
    await capture("settings-light", 390, 844);
    await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("system");
    await page.emulateMedia({
      colorScheme: "dark",
      reducedMotion: "reduce",
      forcedColors: "active",
    });
    await page
      .getByRole("button", { name: "Overview", exact: true })
      .filter({ visible: true })
      .click();
    await capture("overview-forced", 390, 844);
    assert.equal(
      await page.getByRole("button", { name: "Launch Codex", exact: true }).evaluate((button) => {
        const style = getComputedStyle(button);
        return style.color === style.backgroundColor;
      }),
      false,
    );
    await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce", forcedColors: "none" });
    await capture("overview-reflow", 720, 500);
    check(
      "wide/medium/narrow navigation, system/light/forced colors, reduced motion and 720px reflow",
    );
    await page.setViewportSize({ width: 1440, height: 1000 });
    // Axe is injected by the test harness, never shipped as a runtime app asset.
    await page.evaluate(axe.source);
    const accessibility = await page.evaluate(async () =>
      window.axe.run(document, {
        runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"] },
      }),
    );
    writeFileSync(join(output, "accessibility.json"), JSON.stringify(accessibility, null, 2));
    assert.deepEqual(
      accessibility.violations.map((v) => ({ id: v.id, nodes: v.nodes.length })),
      [],
    );
    results.axeViolations = accessibility.violations.length;
    check("automated WCAG checks");
    await choose("Personal");
    for (const mode of [
      "partial",
      "missing",
      "future",
      "contradictory",
      "zero",
      "offline",
      "reauth",
    ]) {
      await scenario(mode);
      const text = await page.locator("main").innerText();
      if (mode === "missing") assert.match(text, /Unsupported/);
      if (mode === "future") {
        assert.match(text, /100%/);
        assert.doesNotMatch(text, /must-not-survive|newBucket|rawResponse/);
      }
      if (mode === "partial") assert.match(text, /75%/);
      if (["partial", "missing", "reauth"].includes(mode))
        assert.match(text, /Last-known remaining/);
      if (mode === "contradictory") {
        assert.match(text, /Conflicting observations|Contradictory/);
        assert.match(
          await page
            .getByRole("heading", { name: "Secondary window", exact: true })
            .first()
            .locator("..")
            .innerText(),
          /60%/,
        );
      }
      if (mode === "zero") assert.match(text, /0%/);
      if (mode === "offline") assert.match(text, /Refresh failed/);
      if (mode === "reauth") assert.match(text, /Reauthentication required|Not eligible/);
      await capture(mode, 1440, 1000);
      check(`${mode} provider fixture remains explicit`);
    }
    // Session-expiry checks advance only the fixture's server clock.
    writeFileSync(control, "expired");
    await page.getByRole("button", { name: "Refresh", exact: true }).click();
    await page.getByRole("heading", { name: "Relaunch CodexFolio", exact: true }).waitFor();
    assert.equal(
      await page.getByRole("heading", { name: "Current capacity", exact: true }).count(),
      0,
    );
    check("expired authorization hides protected evidence and offers local relaunch");
    writeFileSync(control, "supported");
    const replay = await context.newPage();
    await replay.goto(link);
    await replay.getByRole("heading", { name: "Relaunch CodexFolio", exact: true }).waitFor();
    assert.ok(!replay.url().includes("bootstrap="));
    await replay.close();
    const missing = await context.newPage();
    await missing.goto(new URL("/", link).href);
    await missing
      .getByText("Open a one-time dashboard link from your terminal.", { exact: true })
      .waitFor();
    await missing.close();
    check("missing bootstrap has actionable terminal guidance; CSRF-less mutation rejected");
    assert.deepEqual(errors, []);
    assert.deepEqual(remote, []);
    check("bootstrap replay rejected; no runtime errors or remote assets");
  }
} finally {
  writeFileSync(control, "supported");
  writeFileSync(
    join(output, phase === "reentry" ? "reentry-results.json" : "results.json"),
    JSON.stringify(results, null, 2),
  );
  await browser.close();
}
