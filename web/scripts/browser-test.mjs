/* global document, window, innerWidth, getComputedStyle, fetch */
import { URL } from "node:url";
import { performance } from "node:perf_hooks";
import assert from "node:assert/strict";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { cpus, release, totalmem, tmpdir } from "node:os";
import { chromium } from "playwright";
import axe from "axe-core";
import { testSessions } from "./sessions-browser-test.mjs";
import { testAnalytics } from "./analytics-browser-test.mjs";

const [link, control, phase = "deep", referencedHome] = process.argv.slice(2);
assert.ok(
  ["deep", "smoke", "benchmark", "reentry", "health-locked", "health-recovery"].includes(phase),
  "unknown browser phase",
);
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
  ...(phase === "benchmark" ? { locale: "en-US", timezoneId: "UTC" } : {}),
});
const page = await context.newPage();
const cdp = await context.newCDPSession(page);
page.setDefaultTimeout(60_000);
const errors = [],
  remote = [];
page.on("pageerror", (error) => errors.push(error.message));
page.on("request", (request) => {
  if (new URL(request.url()).origin !== new URL(link).origin) remote.push(request.url());
});
const results = {
  suite: phase,
  browser: browser.version(),
  platform: process.platform,
  architecture: process.arch,
  kernel: release(),
  runnerImage: process.env.ImageOS,
  runnerImageVersion: process.env.ImageVersion,
  sourceRevision: process.env.GITHUB_SHA,
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
async function choose(value, confirm = true) {
  const changed =
    value === "*"
      ? null
      : page.waitForResponse(
          (response) =>
            response.url().endsWith("/api/v1/selection") && response.request().method() === "PUT",
        );
  const confirmation =
    changed && confirm
      ? page
          .getByText("Selected Profile updated for future interactive launches.", { exact: true })
          .waitFor()
      : null;
  await scope().selectOption(value);
  if (changed) await changed;
  await page.waitForFunction(
    (expected) =>
      [...document.querySelectorAll('select[aria-label="Dashboard Scope"]')].every(
        (select) => select.value === expected && !select.disabled,
      ),
    value,
  );
  if (confirmation) await confirmation;
}
async function prepareHandoff() {
  await page.getByRole("button", { name: "Prepare Handoff", exact: true }).first().click();
  await assertFocusedHeading("Choose the source project");
  const capture = page.getByRole("button", { name: "Capture checkpoint", exact: true });
  assert.equal(await capture.isDisabled(), true);
  await page
    .getByRole("combobox", { name: "Source Project Identity", exact: true })
    .selectOption({ index: 1 });
  await capture.click();
  await assertFocusedHeading("Prepare Handoff");
}
async function scenario(mode) {
  writeFileSync(control, mode);
  const refreshButton = page.getByRole("button", { name: "Refresh", exact: true });
  const refreshed = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/v1/usage/refresh" &&
      response.request().method() === "POST",
  );
  const reloaded = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/v1/analytics" &&
      response.request().method() === "GET",
  );
  await refreshButton.click();
  await refreshed;
  const response = await reloaded;
  await response.finished();
  await page.evaluate(
    () =>
      new Promise((resolve) =>
        window.requestAnimationFrame(() => window.requestAnimationFrame(resolve)),
      ),
  );
  await page.waitForFunction(() =>
    [...document.querySelectorAll("button")].some(
      (button) => button.textContent?.trim() === "Refresh" && !button.disabled,
    ),
  );
}
async function profileAction(name) {
  const completed = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/profiles") && response.request().method() === "POST",
  );
  await page.getByRole("button", { name, exact: true }).click();
  const response = await completed;
  assert.equal(response.status(), 200, await response.text());
}
async function lifecycleAction(name, action) {
  const completed = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/profile-lifecycle") &&
      response.request().method() === "POST" &&
      response.request().postDataJSON().action === action,
  );
  await page.getByRole("button", { name, exact: true }).click();
  return completed;
}
async function configurationAction(name, action) {
  const completed = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/configuration-packs") &&
      response.request().method() === "POST" &&
      response.request().postDataJSON().action === action,
  );
  await page.getByRole("button", { name, exact: true }).click();
  return completed;
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
async function scanAccessibility(name) {
  const accessibility = await page.evaluate(async () =>
    window.axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21aa", "wcag22aa"] },
    }),
  );
  writeFileSync(
    join(output, name === "overview" ? "accessibility.json" : `accessibility-${name}.json`),
    JSON.stringify(accessibility, null, 2),
  );
  assert.deepEqual(
    accessibility.violations.map((violation) => ({
      id: violation.id,
      nodes: violation.nodes.length,
    })),
    [],
  );
  results.axeViolations = (results.axeViolations ?? 0) + accessibility.violations.length;
  check(`automated WCAG checks: ${name}`);
}
async function assertFocusedHeading(name) {
  const heading = page.getByRole("heading", { name, exact: true });
  await heading.waitFor();
  await page.waitForFunction(
    (expected) =>
      document.activeElement?.tagName === "H1" &&
      document.activeElement.textContent?.trim() === expected,
    name,
  );
}
try {
  if (phase === "health-locked" || phase === "health-recovery") {
    await page.goto(link);
    const recovery = phase === "health-recovery";
    await assertFocusedHeading(
      recovery ? "Local data needs recovery" : "Unlock for this user session",
    );
    assert.ok(!page.url().includes("bootstrap="));
    const main = await page.locator("main").innerText();
    if (recovery) {
      assert.match(main, /preserved the database and stopped writes/);
      assert.match(main, /codex-folio service recovery verify/);
      assert.match(main, /codex-folio service recovery list/);
    } else {
      assert.match(main, /Sensitive collection is paused/);
      assert.match(main, /codex-folio vault unlock/);
      assert.equal(await page.locator('input[type="password"]').count(), 0);
    }
    const stateRequest = await page.request.get(new URL("/api/v1/analytics", link).href);
    assert.equal(stateRequest.status(), 423);
    await page.evaluate(axe.source);
    await scanAccessibility(phase);
    await capture(`${phase}-wide`, 1440, 1000);
    await capture(`${phase}-narrow`, 390, 844);
    await page.emulateMedia({
      colorScheme: "dark",
      reducedMotion: "reduce",
      forcedColors: "active",
    });
    await capture(`${phase}-forced`, 390, 844);
    check(`${phase}: authenticated safe health, state suppression, accessibility and reflow`);
  } else if (phase === "benchmark") {
    const start = performance.now();
    await page.goto(link);
    await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
    results.cachedEvidenceMs = performance.now() - start;
    assert.ok(!page.url().includes("bootstrap="));
    await page.waitForFunction(() => !document.querySelector("select")?.disabled);
    check("isolated cached evidence startup measured; budget evaluated over repeated samples");
  } else if (phase === "reentry") {
    await page.goto(link);
    await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
    const capacityGauges = page.locator('main svg[viewBox="0 0 240 175"]');
    assert.equal(await capacityGauges.count(), 2);
    assert.ok(
      await capacityGauges.evaluateAll((gauges) =>
        gauges.every((gauge) =>
          [...gauge.querySelectorAll("path")].every((path) => path.getBBox().y >= 6),
        ),
      ),
      "capacity gauge arc geometry must keep its complete stroke inside the SVG viewport",
    );
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
    check(
      "fresh reused-service entry succeeds; unavailable loopback connection has terminal guidance",
    );
  } else {
    // Fresh browser has no authority, even though it can load the offline shell.
    const noAuth = await page.request.get(new URL("/api/v1/analytics", link).href);
    assert.equal(noAuth.status(), 401);
    await page.goto(link);
    await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
    assert.ok(!page.url().includes("bootstrap="));
    await page
      .getByText("Refresh failed. Last-known values keep their original capture times.", {
        exact: true,
      })
      .waitFor();
    assert.match(await page.locator("main").innerText(), /1[78] minutes ago/);
    assert.equal(await page.getByText("Recommended", { exact: true }).count(), 0);
    check(
      "one-time bootstrap stripped; stale cached evidence loaded; failed open refresh retains ages",
    );
    assert.equal(await page.title(), "VenkataSudha CodexFolio");
    assert.equal(await page.locator("vite-error-overlay").count(), 0);
    const noCSRF = await page.request.put(new URL("/api/v1/selection", link).href, {
      data: { alias: "Personal" },
      headers: { Origin: new URL(link).origin },
    });
    assert.equal(noCSRF.status(), 403);
    if (phase === "deep") await capture("stale-wide", 1440, 1000);
    await choose("Personal");
    await page
      .getByText("Selected Profile updated for future interactive launches.", { exact: true })
      .waitFor();
    await page
      .getByRole("heading", { name: "Your running Launch Profile is unchanged", exact: true })
      .waitFor();
    check("authenticated profile selection succeeds without changing the running launch");
    if (phase === "deep") {
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
      const projectId = (
        await (await page.request.get(new URL("/api/v1/projects", link).href)).json()
      ).projects[0].project_id;
      const launchesBefore = (
        await (await page.request.get(new URL("/api/v1/analytics", link).href)).json()
      ).activity.length;
      await page.getByRole("button", { name: "Launch Codex", exact: true }).click();
      await assertFocusedHeading("Launch prepared in your terminal");
      await page
        .getByRole("combobox", { name: "Project", exact: true })
        .selectOption({ label: "Atlas · atlas" });
      await page.waitForFunction(
        (expected) => document.querySelector("main")?.textContent?.includes(expected),
        `codex-folio launch Personal --project ${projectId} --`,
      );
      assert.match(await page.locator("main").innerText(), /Prepared · Not started/);
      // Axe is injected by the test harness, never shipped as a runtime app asset.
      await page.evaluate(axe.source);
      await scanAccessibility("launch");
      await capture("launch-wide", 1440, 1000);
      await capture("launch-narrow", 390, 844);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await page.getByRole("button", { name: "Back", exact: true }).click();
      const launchesAfterCancel = (
        await (await page.request.get(new URL("/api/v1/analytics", link).href)).json()
      ).activity.length;
      assert.equal(launchesAfterCancel, launchesBefore);

      await page.getByRole("button", { name: "Launch Codex", exact: true }).click();
      writeFileSync(control, "launch-run-23");
      await page.getByText("Started · Running", { exact: true }).waitFor();
      await choose("Work", false);
      assert.match(
        await page.locator("main").innerText(),
        /Your running Launch Profile is unchanged/,
      );
      assert.match(await page.locator("main").innerText(), /Launch Profile[\s\S]*Personal/);
      writeFileSync(control, "launch-exit-23");
      await page.getByText("Exited · Status 23", { exact: true }).waitFor();
      assert.match(
        await page.locator("main").innerText(),
        /not treated as quota exhaustion by itself/,
      );
      await page.getByRole("button", { name: "Back", exact: true }).click();
      await choose("Personal");
      await page.getByRole("button", { name: "Launch Codex", exact: true }).click();
      writeFileSync(control, "launch-fail");
      await page.getByText("Failed · Codex did not start", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Back", exact: true }).click();
      await choose("Work");
      const handoffActivityBefore = (
        await (await page.request.get(new URL("/api/v1/analytics", link).href)).json()
      ).activity.length;
      await prepareHandoff();
      assert.match(await page.locator("main").innerText(), /Managed Launch is still running/);
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      const handoffActivityAfterCancel = (
        await (await page.request.get(new URL("/api/v1/analytics", link).href)).json()
      ).activity.length;
      assert.equal(handoffActivityAfterCancel, handoffActivityBefore);

      writeFileSync(control, "handoff-source-uncertain");
      await page.waitForTimeout(100);
      await prepareHandoff();
      await page.getByText(/Termination is uncertain/).waitFor();
      await page.getByText("Transcript assistance", { exact: true }).click();
      const reviewCandidates = page.getByRole("button", {
        name: "Review transcript candidates",
        exact: true,
      });
      assert.equal(await reviewCandidates.isDisabled(), true);
      assert.equal(await page.getByLabel("Codex thread ID", { exact: true }).isDisabled(), true);
      check("transcript history remains off until per-handoff consent");

      await page.getByRole("checkbox", { name: /Allow transcript assistance/ }).check();
      await page
        .getByLabel("Codex thread ID", { exact: true })
        .fill("11111111-1111-4111-8111-111111111111");
      await reviewCandidates.click();
      await page.getByText(/Transcript assistance is unavailable/).waitFor();
      assert.equal(await page.locator("#handoff-goal").inputValue(), "");
      assert.match(await page.locator("main").innerText(), /Repository-first checkpoint/);

      writeFileSync(control, "handoff-source-exit");
      await page.waitForTimeout(100);
      await page.getByRole("button", { name: "Check readiness again", exact: true }).click();
      await page.getByText(/Source process: Definitively exited/).waitFor();
      await page
        .getByLabel("Codex thread ID", { exact: true })
        .fill("22222222-2222-4222-8222-222222222222");
      await reviewCandidates.click();
      await page.getByText(/Transcript assistance is unavailable/).waitFor();
      assert.equal(await page.locator("#handoff-goal").inputValue(), "");
      await page
        .getByLabel("Codex thread ID", { exact: true })
        .fill("11111111-1111-4111-8111-111111111111");
      await reviewCandidates.click();
      await page.getByText(/Candidates are transient/).waitFor();
      assert.match(await page.locator("#handoff-goal").inputValue(), /transcript-private-sentinel/);
      await page.locator("#handoff-goal").fill("Finish sanitized assisted release");
      await page.getByText("Repository evidence and redaction", { exact: true }).click();
      await page
        .getByLabel("Redact exact text (one value per line)")
        .fill("transcript-private-sentinel");
      await page.getByRole("button", { name: "Review sanitized preview", exact: true }).click();
      await page.getByText(/Sanitized preview ready/).waitFor();
      assert.doesNotMatch(
        await page.locator("#handoff-risks").inputValue(),
        /transcript-private-sentinel/,
      );
      assert.match(await page.locator("#handoff-risks").inputValue(), /\[REDACTED\]/);
      await page.getByRole("button", { name: "Cancel transcript assistance", exact: true }).click();
      assert.equal(await page.locator("#handoff-goal").inputValue(), "");
      assert.match(await page.locator("main").innerText(), /Repository-first checkpoint/);

      await page.getByRole("checkbox", { name: /Allow transcript assistance/ }).check();
      await page
        .getByLabel("Codex thread ID", { exact: true })
        .fill("11111111-1111-4111-8111-111111111111");
      await reviewCandidates.click();
      await page.locator("#handoff-goal").fill("Finish sanitized assisted release");
      await page
        .locator("#handoff-completed_work")
        .fill("Repository and bounded transcript reviewed");
      await page.locator("#handoff-pending_work").fill("Validate fresh target lifecycle");
      await page.locator("#handoff-known_validation").fill("Focused browser handoff check passed");
      await page.locator("#handoff-risks").fill("transcript-private-sentinel must be removed");
      await page.locator("#handoff-next_action").fill("Run approved checkpoint");
      await page
        .getByLabel("Redact exact text (one value per line)")
        .fill("transcript-private-sentinel");
      const sanitizedPreview = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/handoff") &&
          response.request().postDataJSON().action === "preview-assisted",
      );
      await page.getByRole("button", { name: "Review sanitized preview", exact: true }).click();
      const sanitizedPreviewResponse = await sanitizedPreview;
      const approvedCandidate = (await sanitizedPreviewResponse.json()).handoff;
      assert.ok(approvedCandidate);
      const csrf = sanitizedPreviewResponse.request().headers()["x-codexfolio-csrf"];
      const stale = await page.request.post(new URL("/api/v1/handoff", link).href, {
        data: {
          action: "approve-assisted",
          target_alias: "Personal",
          checkpoint_id: approvedCandidate.checkpoint_id,
          revision: "stale-repository-revision",
          preview_revision: approvedCandidate.revision,
          fields: {
            goal: "Finish sanitized assisted release",
            completed_work: "Repository and bounded transcript reviewed",
            pending_work: "Validate fresh target lifecycle",
            known_validation: "Focused browser handoff check passed",
            risks: "[REDACTED] must be removed",
            next_action: "Run approved checkpoint",
          },
        },
        headers: { Origin: new URL(link).origin, "X-CodexFolio-CSRF": csrf },
      });
      assert.equal(stale.status(), 409, await stale.text());
      await page.setViewportSize({ width: 640, height: 500 });
      await cdp.send("Emulation.setPageScaleFactor", { pageScaleFactor: 2 });
      await page.getByRole("button", { name: "Approve sanitized preview", exact: true }).focus();
      assert.equal(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
        ),
        true,
      );
      assert.equal(
        await page
          .getByRole("button", { name: "Approve sanitized preview", exact: true })
          .isVisible(),
        true,
      );
      await cdp.send("Emulation.setPageScaleFactor", { pageScaleFactor: 1 });
      await page.setViewportSize({ width: 1440, height: 1000 });
      const approval = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/handoff") &&
          response.request().postDataJSON().action === "approve-assisted",
      );
      await page.getByRole("button", { name: "Approve sanitized preview", exact: true }).click();
      const approvalResponse = await approval;
      const approved = (await approvalResponse.json()).handoff;
      assert.ok(approved);
      assert.equal(approved.source, "transcript-assisted");
      assert.match(
        await page.locator("main").innerText(),
        /Approved · Terminal launch not started/,
      );
      assert.match(
        await page.locator("main").innerText(),
        /codex-folio handoff Personal --checkpoint/,
      );
      assert.doesNotMatch(
        await page.locator("main").innerText(),
        /CODEX_HOME|browser-fixture-command/,
      );
      await scanAccessibility("handoff");
      await capture("handoff-wide", 1440, 1000);
      await capture("handoff-narrow", 390, 844);
      await page.emulateMedia({
        colorScheme: "dark",
        reducedMotion: "reduce",
        forcedColors: "active",
      });
      await capture("handoff-forced", 390, 844);
      await page.emulateMedia({
        colorScheme: "dark",
        reducedMotion: "reduce",
        forcedColors: "none",
      });
      await page.setViewportSize({ width: 1440, height: 1000 });
      const checkpointState = async (expected, checkpointId = approved.checkpoint_id) => {
        await page.waitForFunction(
          async ({ origin, csrfToken, checkpointId, expectedState }) => {
            const response = await fetch(`${origin}/api/v1/handoff`, {
              method: "POST",
              headers: { "Content-Type": "application/json", "X-CodexFolio-CSRF": csrfToken },
              body: JSON.stringify({ action: "list" }),
            });
            if (!response.ok) return false;
            const result = await response.json();
            return result.management?.checkpoints?.some(
              (checkpoint) =>
                checkpoint.checkpoint_id === checkpointId && checkpoint.state === expectedState,
            );
          },
          {
            origin: new URL(link).origin,
            csrfToken: csrf,
            checkpointId,
            expectedState: expected,
          },
        );
      };
      writeFileSync(
        control,
        `handoff-recovery-same:${approved.checkpoint_id}:${approved.revision}`,
      );
      const recoveryCheckpoint = `${approved.checkpoint_id}-recovery`;
      await checkpointState("start uncertain", recoveryCheckpoint);
      writeFileSync(control, "handoff-recovery-changed");
      await checkpointState("recoverable", recoveryCheckpoint);
      check(
        "same-boot uncertain start stays non-authorizing; changed boot restores recoverable state",
      );
      writeFileSync(control, `handoff-run-0:${approved.checkpoint_id}:${approved.revision}`);
      await page.getByText("Started · Running", { exact: true }).waitFor();
      writeFileSync(control, "handoff-exit-0");
      await page.getByText("Exited · Status · 0", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      writeFileSync(control, "handoff-restore-running");
      await page.waitForTimeout(100);
      await page.getByRole("button", { name: "Open details", exact: true }).click();
      assert.match(await page.locator("main").innerText(), /codex.primary.used_percent/);
      await page.getByRole("button", { name: "Open details", exact: true }).click();
      check(
        "foreground launch plus browser capture, redaction, revision approval, cancellation and fresh-target lifecycle",
      );
      await capture("running-wide", 1440, 1000);
      await choose("Work");
      await capture("overview-wide", 1440, 1000);
      await testSessions({ page, context, link, capture, scanAccessibility, check });
      await testAnalytics({
        page,
        link,
        control,
        capture,
        scanAccessibility,
        check,
        results,
        axeSource: axe.source,
      });
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
      await page
        .getByRole("button", { name: "Alerts", exact: true })
        .filter({ visible: true })
        .click();
      await assertFocusedHeading("Operational alerts");
      assert.match(await page.locator("main").innerText(), /No active operational conditions/);
      const thresholdDetails = page.locator("details").filter({ hasText: "Capacity thresholds" });
      await thresholdDetails.locator("summary").click();
      await page.waitForFunction(() =>
        [...document.querySelectorAll("details")].some(
          (details) => details.open && details.textContent?.includes("Capacity thresholds"),
        ),
      );
      const thresholdSelects = thresholdDetails.locator("select");
      const thresholdInputs = thresholdDetails.locator('input[type="number"]');
      await thresholdSelects.first().selectOption({ label: "Work" });
      await thresholdInputs.first().fill("70");
      await thresholdInputs.nth(1).fill("60");
      const thresholdSaved = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/alerts") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "set_threshold",
      );
      await page.getByRole("button", { name: "Save thresholds", exact: true }).click();
      assert.equal((await thresholdSaved).status(), 200);
      await page.getByRole("heading", { name: "Capacity at warning threshold" }).waitFor();
      assert.match(await page.locator("main").innerText(), /65% remaining/);
      const acknowledged = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/alerts") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "acknowledge",
      );
      await page.getByRole("button", { name: "Acknowledge", exact: true }).click();
      assert.equal((await acknowledged).status(), 200);
      await page.getByText("acknowledged", { exact: true }).waitFor();
      await page.getByRole("tab", { name: "History", exact: true }).click();
      assert.equal(
        await page.getByRole("heading", { name: "Capacity at warning threshold" }).count(),
        1,
      );
      assert.match(
        await page.locator("main").innerText(),
        /Dashboard delivery: available[\s\S]*Native notification delivery: not configured/,
      );
      await scanAccessibility("alerts");
      await capture("alerts-narrow", 390, 844);
      check("Alerts evaluates thresholds, deduplicates, acknowledges and retains bounded history");
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
      const settings = await page.locator("main").innerText();
      assert.match(settings, /Background service[\s\S]*On demand · Not enrolled/);
      assert.match(settings, /Native per-user mechanism: systemd-user/);
      assert.match(settings, /codex-folio service install/);
      check("Settings exposes native enrollment status and explicit terminal guidance");
      assert.match(settings, /Periodic collection schedule[\s\S]*On demand · Saved intervals/);
      assert.equal(
        await page.getByLabel("Managed Launch interval · minutes", { exact: true }).inputValue(),
        "5",
      );
      assert.equal(
        await page.getByLabel("Idle interval · minutes", { exact: true }).inputValue(),
        "30",
      );
      const scheduleSaved = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/collection-settings") &&
          response.request().method() === "PUT",
      );
      await page.getByLabel("Idle interval · minutes", { exact: true }).fill("45");
      await page.getByRole("button", { name: "Save collection intervals", exact: true }).click();
      assert.equal((await scheduleSaved).status(), 200);
      await page.getByText("Periodic collection intervals saved.", { exact: true }).waitFor();
      const persistedSchedule = await page.request.get(
        new URL("/api/v1/collection-settings", link).href,
      );
      assert.equal(persistedSchedule.status(), 200);
      assert.equal((await persistedSchedule.json()).idle_interval_seconds, 2700);
      assert.equal(
        await page.getByLabel("Idle interval · minutes", { exact: true }).inputValue(),
        "45",
      );
      check("Settings persists bounded collection intervals without enrolling the service");
      await page.getByText("Checkpoint inventory loaded.", { exact: true }).waitFor();
      assert.match(await page.locator("main").innerText(), /Completed/);
      assert.match(await page.locator("main").innerText(), /Retained/);
      assert.match(await page.locator("main").innerText(), /Expired/);
      await page.getByLabel("Repository-first retention", { exact: true }).fill("31");
      await page.getByLabel("Transcript-assisted retention", { exact: true }).fill("unlimited");
      await page.getByRole("button", { name: "Save retention", exact: true }).click();
      await page.getByText("Retention policy saved.", { exact: true }).waitFor();
      await page.setViewportSize({ width: 640, height: 500 });
      await cdp.send("Emulation.setPageScaleFactor", { pageScaleFactor: 2 });
      await page.getByRole("button", { name: "Save retention", exact: true }).focus();
      assert.equal(
        await page.evaluate(
          () => document.documentElement.scrollWidth <= document.documentElement.clientWidth,
        ),
        true,
      );
      assert.equal(
        await page.getByRole("button", { name: "Save retention", exact: true }).isVisible(),
        true,
      );
      await cdp.send("Emulation.setPageScaleFactor", { pageScaleFactor: 1 });
      await page.setViewportSize({ width: 390, height: 844 });

      await page.getByLabel("Encrypted .cfolio", { exact: true }).check();
      await page.getByRole("button", { name: "Preview export", exact: true }).first().click();
      await page.getByText(/Always excluded/).waitFor();
      const exportPreviewText = await page.locator("main").innerText();
      assert.match(exportPreviewText, /Raw transcripts/);
      assert.match(exportPreviewText, /Identity Homes/);
      assert.doesNotMatch(exportPreviewText, /raw_transcripts|identity_homes/);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await capture("checkpoint-export-wide", 1440, 1000);
      await page.setViewportSize({ width: 390, height: 844 });
      await page.getByLabel("Export passphrase", { exact: true }).fill("browser-export-passphrase");
      const encryptedDownload = page.waitForEvent("download");
      await page.getByRole("button", { name: "Download export", exact: true }).click();
      const encrypted = await encryptedDownload;
      assert.match(encrypted.suggestedFilename(), /\.cfolio$/);
      const encryptedPath = await encrypted.path();
      assert.ok(encryptedPath);
      const encryptedContents = readFileSync(encryptedPath, "utf8");
      assert.match(encryptedContents, /codex-folio\.cfolio\.v1/);
      assert.doesNotMatch(encryptedContents, /transcript-private-sentinel|browser-fixture-command/);

      await page.getByLabel("Plaintext JSON", { exact: true }).check();
      await page.getByRole("button", { name: "Preview export", exact: true }).first().click();
      const plaintextDownloadButton = page.getByRole("button", {
        name: "Download export",
        exact: true,
      });
      assert.equal(await plaintextDownloadButton.isDisabled(), true);
      await page.getByRole("checkbox", { name: /I understand plaintext is not encrypted/ }).check();
      const plaintextDownload = page.waitForEvent("download");
      await plaintextDownloadButton.click();
      const plaintext = await plaintextDownload;
      assert.match(plaintext.suggestedFilename(), /\.json$/);
      const plaintextPath = await plaintext.path();
      assert.ok(plaintextPath);
      const plaintextContents = readFileSync(plaintextPath, "utf8");
      assert.match(plaintextContents, /Finish sanitized assisted release/);
      assert.doesNotMatch(
        plaintextContents,
        /transcript-private-sentinel|browser-fixture-command|CODEX_HOME/,
      );

      const rowsBeforePurge = await page.locator("article").count();
      await page.getByRole("button", { name: "Preview purge", exact: true }).first().click();
      assert.equal(
        await page.getByRole("button", { name: "Purge exact revision", exact: true }).isDisabled(),
        true,
      );
      await page.getByRole("button", { name: "Cancel purge", exact: true }).click();
      assert.equal(await page.locator("article").count(), rowsBeforePurge);
      await page.getByRole("button", { name: "Preview purge", exact: true }).first().click();
      await page.getByLabel("Type PURGE to confirm", { exact: true }).fill("PURGE");
      await page.getByRole("button", { name: "Purge exact revision", exact: true }).click();
      await page.getByText("Checkpoint inventory loaded.", { exact: true }).waitFor();
      assert.equal(await page.locator("article").count(), rowsBeforePurge - 1);
      await scanAccessibility("checkpoint-management");
      await capture("checkpoint-management-narrow", 390, 844);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await capture("checkpoint-management-wide", 1440, 1000);
      check(
        "checkpoint retention, encrypted and acknowledged plaintext export, cancel and exact purge",
      );
      await page.setViewportSize({ width: 390, height: 844 });
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
      assert.match(await page.locator("main").innerText(), /Decision-relevant alerts/);
      await capture("overview-forced", 390, 844);
      assert.equal(
        await page.getByRole("button", { name: "Launch Codex", exact: true }).evaluate((button) => {
          const style = getComputedStyle(button);
          return style.color === style.backgroundColor;
        }),
        false,
      );
      await page.emulateMedia({
        colorScheme: "dark",
        reducedMotion: "reduce",
        forcedColors: "none",
      });
      await capture("overview-reflow", 720, 500);
      check(
        "wide/medium/narrow navigation, system/light/forced colors, reduced motion and 720px reflow",
      );
      await page.setViewportSize({ width: 1440, height: 1000 });
      await scanAccessibility("overview");
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Profiles", exact: true })
        .click();
      await assertFocusedHeading("Profiles");
      await scanAccessibility("profiles");
      assert.doesNotMatch(await page.locator("main").innerText(), /referenced-home|CODEX_HOME/);

      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await assertFocusedHeading("Add Identity Profile");
      await scanAccessibility("profile-setup");
      await page.getByLabel("Display Name", { exact: true }).fill("Research");
      await page.getByLabel("CLI Alias", { exact: true }).fill("Research");
      await profileAction("Save and close");
      await page.getByText("Pending · Resume setup", { exact: true }).first().waitFor();
      await page.getByRole("button", { name: "Pending · Resume setup", exact: true }).click();
      await profileAction("Continue in Codex");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();
      assert.match(await page.locator("main").innerText(), /Found · 0\.153\.4/);
      assert.doesNotMatch(await page.locator("main").innerText(), /browser-auth-secret/);
      await page.getByRole("button", { name: "Cancel", exact: true }).click();

      await page.getByRole("button", { name: "Work", exact: true }).click();
      await page.getByRole("button", { name: "Manage Shared Configuration", exact: true }).click();
      await assertFocusedHeading("Shared Configuration Packs");
      assert.doesNotMatch(await page.locator("main").innerText(), /model =|gpt-5-mini/);
      const projectionPreview = await configurationAction("Preview projection", "preview");
      const projectionPreviewBody = await projectionPreview.text();
      assert.equal(projectionPreview.status(), 200, projectionPreviewBody);
      assert.deepEqual(JSON.parse(projectionPreviewBody).plan?.conflicts, [
        { path: "config.toml", kind: "modified" },
      ]);
      await page
        .getByText("These profile-local files will be preserved:", { exact: true })
        .waitFor();
      assert.match(await page.locator("main").innerText(), /config\.toml · modified/);
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      assert.equal(
        await page.getByRole("button", { name: "Apply reviewed projection", exact: true }).count(),
        0,
      );
      await configurationAction("Preview projection", "preview");
      const blockedProjection = await configurationAction("Apply reviewed projection", "apply");
      assert.equal(blockedProjection.status(), 500);
      await page
        .getByText("Projection was not applied. Local configuration remains preserved.", {
          exact: true,
        })
        .waitFor();
      await page.getByLabel("New pack version", { exact: true }).fill("2");
      await configurationAction("Preview promotion", "promotion-preview");
      await page
        .getByText("The new version contains these reviewed changes:", { exact: true })
        .waitFor();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      assert.doesNotMatch(await page.locator("main").innerText(), /shared · 2/);
      await configurationAction("Preview promotion", "promotion-preview");
      const promoted = await configurationAction("Create reviewed version", "promote");
      assert.equal(promoted.status(), 200, await promoted.text());
      await page
        .getByText(
          "Reviewed immutable pack version created. The profile assignment is unchanged.",
          {
            exact: true,
          },
        )
        .waitFor();
      assert.match(await page.locator("main").innerText(), /shared · 2/);
      await scanAccessibility("configuration-packs");
      await capture("configuration-packs-wide", 1440, 1000);
      await capture("configuration-packs-narrow", 390, 844);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await page.getByRole("button", { name: "Back to Profiles", exact: true }).click();

      await page.getByRole("button", { name: "Research", exact: true }).click();
      await page.getByRole("button", { name: "Manage Shared Configuration", exact: true }).click();
      await page
        .getByRole("combobox", { name: "Approved pack version", exact: true })
        .selectOption("shared@1");
      const assignedPack = await configurationAction("Assign reviewed version", "assign");
      assert.equal(assignedPack.status(), 200, await assignedPack.text());
      await configurationAction("Preview projection", "preview");
      await page.getByText("No profile-local conflicts detected.", { exact: true }).waitFor();
      const projectionApplied = page
        .getByText("Reviewed projection applied; profile-local conflicts were preserved.", {
          exact: true,
        })
        .waitFor();
      const applied = await configurationAction("Apply reviewed projection", "apply");
      assert.equal(applied.status(), 200, await applied.text());
      await projectionApplied;
      await page.getByRole("button", { name: "Back to Profiles", exact: true }).click();
      check(
        "configuration assignment, conflict preview, cancellation, rejected application, reviewed promotion and successful projection",
      );

      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await page.getByLabel("Display Name", { exact: true }).fill("Referenced");
      await page.getByLabel("CLI Alias", { exact: true }).fill("Referenced");
      await page.getByLabel("Reference an existing Identity Home", { exact: true }).check();
      await page.getByLabel("Existing Identity Home path", { exact: true }).fill(referencedHome);
      await profileAction("Continue in Codex");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      assert.doesNotMatch(await page.locator("main").innerText(), new RegExp(referencedHome));

      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await page.getByLabel("Display Name", { exact: true }).fill("Device");
      await page.getByLabel("CLI Alias", { exact: true }).fill("Device");
      await page.getByLabel("Device code", { exact: true }).check();
      await profileAction("Continue in Codex");
      await page
        .getByText("Continue device-code authentication in your terminal:", { exact: true })
        .waitFor();
      assert.match(
        await page.locator("main").innerText(),
        /codex-folio profile add Device --device-code/,
      );
      await page.getByRole("button", { name: "Cancel", exact: true }).click();

      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await page.getByLabel("Display Name", { exact: true }).fill("Failure");
      await page.getByLabel("CLI Alias", { exact: true }).fill("Failure");
      writeFileSync(control, "profile-auth-fail");
      const failed = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/profiles") && response.request().method() === "POST",
      );
      await page.getByRole("button", { name: "Continue in Codex", exact: true }).click();
      assert.equal((await failed).status(), 409);
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      const failedProfile = page.getByRole("button", { name: "Failure", exact: true });
      await failedProfile.waitFor();
      await failedProfile.click();
      assert.match(await page.locator("main").innerText(), /Pending · Resume setup/);

      await page.getByRole("button", { name: "Work", exact: true }).click();
      await page.getByRole("button", { name: "Edit", exact: true }).click();
      await page.getByLabel("Display Name", { exact: true }).fill("Work Studio");
      await page.getByRole("button", { name: "Save changes", exact: true }).click();
      await page.getByRole("heading", { name: "Work Studio", exact: true }).waitFor();
      writeFileSync(control, "profile-needs-auth");
      await page.getByRole("button", { name: "Reauthenticate", exact: true }).click();
      await assertFocusedHeading("Reauthenticate Work Studio");
      await scanAccessibility("profile-reauthentication");
      await capture("reauth-wide", 1440, 1000);
      await capture("reauth-narrow", 390, 844);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await profileAction("Continue in Codex");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      await page.getByRole("button", { name: "Personal", exact: true }).click();
      const selectedProfile = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/selection") && response.request().method() === "PUT",
      );
      await page.getByRole("button", { name: "Select", exact: true }).click();
      assert.equal((await selectedProfile).status(), 200);
      await page.getByText("Ready · Selected", { exact: true }).first().waitFor();
      await capture("profiles-wide", 1440, 1000);
      await capture("profiles-narrow", 390, 844);
      await capture("profiles-reflow", 720, 1000);
      check(
        "profile setup, resume, referenced home, device handoff, failure, edit, selection and reauthentication",
      );

      await page.setViewportSize({ width: 1440, height: 1000 });
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Overview", exact: true })
        .click();
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
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Profiles", exact: true })
        .click();
      await assertFocusedHeading("Profiles");

      await page.getByRole("button", { name: "Work Studio", exact: true }).click();
      await page.getByText("Removal and recovery", { exact: true }).click();
      await lifecycleAction("Review local removal", "preview");
      assert.equal(
        await page.getByRole("heading", { name: "Review local removal", exact: true }).count(),
        1,
        await page.locator("main").innerText(),
      );
      await page.getByLabel("Type CLI Alias to confirm", { exact: true }).fill("Work");
      const blockedRemoval = lifecycleAction("Confirm local removal", "remove");
      assert.equal((await blockedRemoval).status(), 409);
      await page
        .getByText("Removal is blocked while this Identity Profile has a running Managed Launch.", {
          exact: true,
        })
        .waitFor();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();

      await page.getByRole("button", { name: "Research", exact: true }).click();
      await page.getByText("Removal and recovery", { exact: true }).click();
      await lifecycleAction("Review local removal", "preview");
      await page.getByLabel("Type CLI Alias to confirm", { exact: true }).fill("Research");
      const quarantined = page
        .getByText("Identity Profile moved to seven-day local quarantine.", { exact: true })
        .waitFor();
      const quarantine = await lifecycleAction("Confirm local removal", "remove");
      assert.equal(quarantine.status(), 200, await quarantine.text());
      await quarantined;
      assert.match(await page.locator("main").innerText(), /Recoverable until/);
      const restored = page
        .getByText("Identity Profile restored from local quarantine.", { exact: true })
        .waitFor();
      await lifecycleAction("Restore", "restore");
      await restored;

      await page.getByRole("button", { name: "Research", exact: true }).click();
      await page.getByText("Removal and recovery", { exact: true }).click();
      await lifecycleAction("Review local removal", "preview");
      await page.getByLabel("Type CLI Alias to confirm", { exact: true }).fill("Research");
      const quarantinedAgain = page
        .getByText("Identity Profile moved to seven-day local quarantine.", { exact: true })
        .waitFor();
      await lifecycleAction("Confirm local removal", "remove");
      await quarantinedAgain;
      await page.getByRole("button", { name: "Purge permanently", exact: true }).click();
      await assertFocusedHeading("Review final local purge");
      await page.getByLabel("Type CLI Alias to confirm", { exact: true }).fill("Research");
      await lifecycleAction("Confirm final purge", "purge");
      await page
        .getByText("Quarantined local profile permanently purged.", { exact: true })
        .waitFor();

      await choose("Work", false);
      await choose("Personal", false);
      await page.getByRole("button", { name: "Personal", exact: true }).click();
      await page.getByText("Removal and recovery", { exact: true }).click();
      await lifecycleAction("Review local removal", "preview");
      await page.getByLabel("Type CLI Alias to confirm", { exact: true }).fill("personal");
      assert.equal(
        await page.getByRole("button", { name: "Confirm local removal", exact: true }).isDisabled(),
        true,
      );
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      await page.getByRole("button", { name: "Personal", exact: true }).click();
      await page.getByText("Removal and recovery", { exact: true }).click();
      await lifecycleAction("Review local removal", "preview");
      await page.getByLabel("Type CLI Alias to confirm", { exact: true }).fill("Personal");
      assert.match(await page.locator("main").innerText(), /Replacement Selected Profile/);
      assert.equal(
        await page.getByRole("button", { name: "Confirm local removal", exact: true }).isDisabled(),
        true,
      );
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      assert.equal(await scope().inputValue(), "Personal");
      assert.match(
        await page
          .getByRole("row")
          .filter({ has: page.getByRole("button", { name: "Personal", exact: true }) })
          .innerText(),
        /Ready · Selected/,
      );
      await page.getByRole("button", { name: "Personal", exact: true }).click();
      await page.getByText("Removal and recovery", { exact: true }).click();
      await lifecycleAction("Review local removal", "preview");
      await page.getByLabel("Type CLI Alias to confirm", { exact: true }).fill("Personal");
      await page.getByLabel("Replacement Selected Profile", { exact: true }).selectOption("Work");
      await scanAccessibility("profile-removal");
      await capture("removal-wide", 1440, 1000);
      await capture("removal-narrow", 390, 844);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await lifecycleAction("Confirm local removal", "remove");
      await page
        .getByText(
          "Local registration removed. The Referenced Identity Home and remote OpenAI identity are unchanged.",
          { exact: true },
        )
        .waitFor();
      assert.match(await page.locator("main").innerText(), /Work Studio/);
      check(
        "managed quarantine, restore and purge; referenced non-ownership; running, selection, exact-confirmation and cancellation protections",
      );
      // Session-expiry checks advance only the fixture's server clock.
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Overview", exact: true })
        .click();
      await page.getByRole("button", { name: "Refresh", exact: true }).waitFor();
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
      check("bootstrap replay rejected");
    }
  }
  assert.deepEqual(errors, []);
  assert.deepEqual(remote, []);
  check("no runtime errors or remote assets");
} finally {
  writeFileSync(control, "supported");
  writeFileSync(join(output, `${phase}-results.json`), JSON.stringify(results, null, 2));
  await browser.close();
}
