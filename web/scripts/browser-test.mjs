/* global document, window, innerWidth, getComputedStyle, fetch */
import { URL } from "node:url";
import { Buffer } from "node:buffer";
import { performance } from "node:perf_hooks";
import { DatabaseSync } from "node:sqlite";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { cpus, release, totalmem, tmpdir } from "node:os";
import { chromium } from "playwright";
import axe from "axe-core";
import { testSessions } from "./sessions-browser-test.mjs";
import { testAnalytics } from "./analytics-browser-test.mjs";
import { testSettingsNarrowReflow, testNarrowFocusVisibility } from "./narrow-browser-checks.mjs";

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
  const started = performance.now();
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
  return performance.now() - started;
}
async function profileAction(name) {
  const completed = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/profiles") && response.request().method() === "POST",
  );
  await page.getByRole("button", { name, exact: true }).click();
  const response = await completed;
  assert.equal(response.status(), 200, await response.text());
  return response;
}
async function runTerminalProfile(alias, method = "browser", expectedCode = "", action = "add") {
  // The harness holds the command token; the browser page never receives it.
  const response = await page.request.post(
    new URL("/api/v1/command/profile-authentication", link).href,
    {
      headers: {
        "X-CodexFolio-Command-Token": "browser-fixture-command",
        Origin: new URL(link).origin,
      },
      data: { action, alias, auth_method: method, non_interactive: true },
    },
  );
  assert.equal(response.status(), 200);
  const events = (await response.text())
    .trim()
    .split("\n")
    .map((line) => JSON.parse(line));
  assert.equal(events.at(-1)?.code ?? "", expectedCode);
  return events;
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
    await page.getByRole("heading", { name: "Trusted browser", exact: true }).waitFor();
    assert.equal(await page.getByRole("button", { name: "Trust this browser" }).count(), 1);
    check("new browser offers explicit trust without granting it automatically");
    if (phase === "smoke") {
      await page.getByRole("button", { name: "Trust this browser" }).click();
      await page.getByText("This browser is trusted. You can revoke it in Settings.").waitFor();
      await page.reload();
      await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
      assert.equal(await page.getByRole("button", { name: "Trust this browser" }).count(), 0);
      const privateContext = await browser.newContext();
      const privatePage = await privateContext.newPage();
      await privatePage.goto(new URL("/", link).href);
      await privatePage.getByText("Open a one-time dashboard link from your terminal.").waitFor();
      await privateContext.close();
      check("trusted browser reopens without bootstrap; private browser remains unauthorized");
    }
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
      results.ordinaryRefreshMs = await scenario("supported");
      assert.ok(
        results.ordinaryRefreshMs < 10_000,
        `ordinary fixture refresh ${results.ordinaryRefreshMs.toFixed(1)}ms exceeds 10s engineering budget`,
      );
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
      const launchProjects = (
        await (await page.request.get(new URL("/api/v1/projects", link).href)).json()
      ).projects;
      const projectId = launchProjects.find((project) => project.alias === "Atlas").project_id;
      const launchesBefore = (
        await (await page.request.get(new URL("/api/v1/analytics", link).href)).json()
      ).activity.length;
      await page.getByRole("button", { name: "Launch Codex", exact: true }).click();
      await assertFocusedHeading("Launch prepared in your terminal");
      const alternateProject = launchProjects.find((project) => project.alias === "Zephyr");
      await page
        .getByRole("combobox", { name: "Project", exact: true })
        .selectOption(alternateProject.project_id);
      assert.match(await page.locator("main").innerText(), /Zephyr · zephyr/);
      assert.ok(
        (await page.locator("main code").innerText()).includes(
          `--project ${alternateProject.project_id}`,
        ),
      );
      await page
        .getByRole("combobox", { name: "Project", exact: true })
        .selectOption({ label: "Atlas · atlas" });
      await page.waitForFunction(
        (expected) => document.querySelector("main")?.textContent?.includes(expected),
        `codex-folio launch Personal --project ${projectId} --`,
      );
      assert.match(await page.locator("main").innerText(), /Prepared · Not started/);
      check(
        "prepared Launch project choice updates alternate and original project guidance without starting Codex",
      );
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
      await choose("Personal");
      await prepareHandoff();
      const repositoryFields = {
        goal: "Review repository-only continuation",
        completed_work: "Inspected local repository",
        pending_work: "Review remaining implementation",
        known_validation: "Browser fixture validation",
        risks: "Review before executing",
        next_action: "Continue after explicit terminal launch",
      };
      for (const [field, value] of Object.entries(repositoryFields)) {
        await page.locator(`#handoff-${field}`).fill(value);
      }
      assert.equal(
        await page.getByRole("button", { name: "Approve this revision", exact: true }).isDisabled(),
        true,
      );
      await page.getByText("Repository evidence and redaction", { exact: true }).click();
      await page.getByRole("checkbox", { name: "handoff-notes.txt", exact: true }).check();
      const repositoryEdit = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/handoff") &&
          response.request().postDataJSON().action === "edit",
      );
      await page.getByRole("button", { name: "Save sanitized draft", exact: true }).click();
      const editedRepository = await repositoryEdit;
      assert.equal(editedRepository.status(), 200, await editedRepository.text());
      assert.deepEqual(editedRepository.request().postDataJSON().fields, repositoryFields);
      assert.deepEqual(editedRepository.request().postDataJSON().redact_paths, [
        "handoff-notes.txt",
      ]);
      const repositoryDraft = (await editedRepository.json()).handoff;
      assert.equal(repositoryDraft.source, "repository-first");
      assert.equal(repositoryDraft.source_state, "exited");
      assert.equal(repositoryDraft.target_eligible, true, repositoryDraft.target_caution);
      assert.ok(!JSON.stringify(repositoryDraft.repository).includes("handoff-notes.txt"));
      await page
        .getByText("Sanitized draft saved · Approval applies only to this revision", {
          exact: true,
        })
        .waitFor();
      assert.equal(
        await page.getByRole("checkbox", { name: "handoff-notes.txt", exact: true }).count(),
        0,
      );
      const repositoryApproval = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/handoff") &&
          response.request().postDataJSON().action === "approve",
      );
      await page.getByRole("button", { name: "Approve this revision", exact: true }).click();
      const approvedRepository = await repositoryApproval;
      assert.equal(approvedRepository.status(), 200, await approvedRepository.text());
      assert.equal(approvedRepository.request().postDataJSON().revision, repositoryDraft.revision);
      assert.equal((await approvedRepository.json()).handoff.source, "repository-first");
      await page.getByText("Approved · Terminal launch not started", { exact: true }).waitFor();
      const activityBeforeRepositoryCancel = await (
        await page.request.get(new URL("/api/v1/activity", link).href)
      ).json();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      const activityAfterRepositoryCancel = await (
        await page.request.get(new URL("/api/v1/activity", link).href)
      ).json();
      assert.deepEqual(activityAfterRepositoryCancel, activityBeforeRepositoryCancel);
      check(
        "repository-only fields save, path redaction, revision approval and cancellation do not launch Codex",
      );
      await choose("Work");
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
      await page.getByRole("heading", { name: "5-hour window", exact: true }).waitFor();
      await page.getByRole("heading", { name: "Weekly window", exact: true }).waitFor();
      check("Overview names actual reported quota durations instead of provider slots");
      await capture("overview-wide", 1440, 1000);
      await testSessions({ page, context, link, control, capture, scanAccessibility, check });
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
      const activeTab = page.getByRole("tab", { name: /^Active/ });
      const historyTab = page.getByRole("tab", { name: "History", exact: true });
      await activeTab.focus();
      await activeTab.press("ArrowRight");
      assert.equal(
        await historyTab.evaluate((element) => element === document.activeElement),
        true,
      );
      assert.equal(await historyTab.getAttribute("aria-selected"), "true");
      assert.equal(await activeTab.getAttribute("tabindex"), "-1");
      assert.equal(
        await page.getByRole("tabpanel").getAttribute("aria-labelledby"),
        await historyTab.getAttribute("id"),
      );
      await historyTab.press("Home");
      await activeTab.press("End");
      await historyTab.press("ArrowLeft");
      assert.equal(await activeTab.evaluate((element) => element === document.activeElement), true);
      check("Alerts tabs support roving focus, arrows, Home/End and panel relationships");
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
      // A rejected action must show feedback without an unhandled page error or clearing edits.
      await page.route("**/api/v1/alerts", async (route) => {
        if (route.request().method() === "POST")
          await route.fulfill({ status: 503, contentType: "application/json", body: "{}" });
        else await route.continue();
      });
      await page.getByRole("button", { name: "Save thresholds", exact: true }).click();
      await page.getByRole("alert").filter({ hasText: "Alert change was not applied." }).waitFor();
      assert.equal(await thresholdInputs.first().inputValue(), "70");
      await page.unroute("**/api/v1/alerts");
      check("Rejected alert saves retain edits and show feedback without unhandled rejection");
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
      await page
        .getByRole("button", { name: "Overview", exact: true })
        .filter({ visible: true })
        .click();
      await page.getByRole("button", { name: "Open Alerts", exact: true }).click();
      await assertFocusedHeading("Operational alerts");
      check("Overview Open Alerts opens focused Operational alerts");
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
      const [detailEnabled] = await Promise.all([
        page.waitForResponse(
          (response) =>
            response.url().endsWith("/api/v1/alerts") &&
            response.request().method() === "POST" &&
            response.request().postDataJSON().action === "set_notification_detail" &&
            response.request().postDataJSON().detailed_content_enabled === true,
        ),
        page
          .getByRole("radio", {
            name: /Include identity and quota details on this device/,
          })
          .check(),
      ]);
      assert.equal(detailEnabled.status(), 200);
      const [detailRevoked] = await Promise.all([
        page.waitForResponse(
          (response) =>
            response.url().endsWith("/api/v1/alerts") &&
            response.request().method() === "POST" &&
            response.request().postDataJSON().action === "set_notification_detail" &&
            response.request().postDataJSON().detailed_content_enabled === false,
        ),
        page.getByRole("radio", { name: /Generic notifications/ }).check(),
      ]);
      assert.equal(detailRevoked.status(), 200);
      assert.match(
        await page.locator("main").innerText(),
        /Dashboard delivery: available[\s\S]*Native notification delivery: not enrolled/,
      );
      await scanAccessibility("alerts");
      await capture("alerts-narrow", 390, 844);
      check(
        "Alerts evaluates thresholds, deduplicates, acknowledges, retains bounded history, and revokes native detail consent",
      );
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
      const appearanceSaved = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "configure_appearance",
      );
      await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("light");
      assert.equal((await appearanceSaved).status(), 200);
      await page.waitForFunction(() => document.documentElement.dataset.theme === "light");
      assert.equal(await page.evaluate(() => document.documentElement.dataset.theme), "light");
      const settings = await page.locator("main").innerText();
      assert.equal(
        await page.getByRole("radio", { name: /Generic notifications/ }).isChecked(),
        true,
      );
      assert.match(
        settings,
        /Notification privacy[\s\S]*separate consent on this device[\s\S]*Generic notifications/,
      );
      assert.match(settings, /Background service[\s\S]*On demand · Not enrolled/);
      assert.match(settings, /Native per-user mechanism: systemd-user/);
      assert.match(settings, /codex-folio service install/);
      check("Settings exposes native enrollment status and explicit terminal guidance");
      assert.match(
        settings,
        /Periodic collection schedule[\s\S]*Periodic collection has not been chosen/,
      );
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
      await page.getByRole("button", { name: "Enable periodic collection", exact: true }).focus();
      await page.keyboard.press("Enter");
      await page.getByText("Periodic collection enabled.", { exact: true }).waitFor();
      assert.equal(
        (await (await page.request.get(new URL("/api/v1/collection-settings", link).href)).json())
          .consent,
        "accepted",
      );
      await page.getByRole("button", { name: "Decline periodic collection", exact: true }).click();
      await page
        .getByText("Periodic collection declined; on-demand refresh remains available.", {
          exact: true,
        })
        .waitFor();
      const declinedSchedule = await (
        await page.request.get(new URL("/api/v1/collection-settings", link).href)
      ).json();
      assert.equal(declinedSchedule.consent, "declined");
      assert.equal(declinedSchedule.scheduler_enabled, false);
      check(
        "Settings consent controls are keyboard-accessible and independent of OS-login enrollment",
      );

      const portableSection = page.locator(
        'section[aria-labelledby="portable-configuration-title"]',
      );
      await portableSection
        .getByLabel("Include Project Aliases matched only by repository basename", {
          exact: true,
        })
        .check();
      const configurationExported = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "export_preview",
      );
      await portableSection
        .getByRole("button", { name: "Preview configuration export", exact: true })
        .click();
      const configurationExportResponse = await configurationExported;
      assert.equal(configurationExportResponse.status(), 200);
      const configurationPreview = await configurationExportResponse.json();
      assert.equal(configurationPreview.preview.schema_version, 1);
      assert.ok(configurationPreview.preview.confirmation_digest);
      assert.equal(configurationPreview.preview.bundle.operational_preferences.appearance, "light");
      assert.ok(configurationPreview.preview.bundle.project_aliases.length > 0);
      const portableBody = JSON.stringify(configurationPreview.preview.bundle);
      assert.doesNotMatch(
        portableBody,
        /identity_home|canonical_path|credential|authentication_method|telemetry|consent|session|raw_content|automatic_update|notification_detail|service_enrollment/,
      );
      const configurationDownload = page.waitForEvent("download");
      await portableSection
        .getByRole("button", { name: "Download reviewed configuration", exact: true })
        .click();
      const downloadedConfiguration = await configurationDownload;
      assert.equal(
        downloadedConfiguration.suggestedFilename(),
        "codex-folio-configuration-v1.json",
      );
      assert.deepEqual(
        JSON.parse(readFileSync(await downloadedConfiguration.path(), "utf8")),
        configurationPreview.preview.bundle,
      );

      const configurationFile = portableSection.getByLabel("Portable configuration JSON", {
        exact: true,
      });
      const profilesBeforeRejectedImports = await page.request.get(
        new URL("/api/v1/profiles", link).href,
      );
      const rejectedImportBaseline = await profilesBeforeRejectedImports.json();
      const configurationCsrf = configurationExportResponse.request().headers()[
        "x-codexfolio-csrf"
      ];
      assert.ok(configurationCsrf);
      const malformedResponse = await page.evaluate(
        async ({ csrfToken }) => {
          const response = await fetch("/api/v1/configuration", {
            method: "POST",
            credentials: "include",
            headers: {
              "Content-Type": "application/json",
              "X-CodexFolio-CSRF": csrfToken,
            },
            body: '{"action":"import_preview","bundle":',
          });
          return { status: response.status, body: await response.json() };
        },
        { csrfToken: configurationCsrf },
      );
      assert.equal(malformedResponse.status, 400);
      assert.equal(malformedResponse.body.code, "CF_CONFIGBUNDLE_INVALID");
      await configurationFile.setInputFiles({
        name: "unsupported-configuration.json",
        mimeType: "application/json",
        buffer: Buffer.from(
          JSON.stringify({
            ...configurationPreview.preview.bundle,
            schema_version: 2,
          }),
        ),
      });
      const unsupportedPreviewed = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "import_preview",
      );
      await portableSection
        .getByRole("button", { name: "Preview configuration import", exact: true })
        .click();
      assert.equal((await unsupportedPreviewed).status(), 400);
      await portableSection
        .getByText(
          "The file is malformed, unsupported, too large, or contains a field outside schema v1.",
          { exact: true },
        )
        .waitFor();
      const profilesAfterRejectedImports = await page.request.get(
        new URL("/api/v1/profiles", link).href,
      );
      assert.deepEqual(await profilesAfterRejectedImports.json(), rejectedImportBaseline);
      const configurationAfterRejectedImports = await page.request.get(
        new URL("/api/v1/configuration?include_project_aliases=true", link).href,
      );
      assert.deepEqual(
        (await configurationAfterRejectedImports.json()).preview.bundle,
        configurationPreview.preview.bundle,
      );

      const conflictingConfiguration = {
        schema_version: 1,
        profiles: [{ alias: "Work", display_name: "Imported Work" }],
        configuration_packs: [],
        alert_thresholds: [],
        operational_preferences: {
          ...configurationPreview.preview.bundle.operational_preferences,
          appearance: "dark",
        },
      };
      await configurationFile.setInputFiles({
        name: "conflicting-configuration.json",
        mimeType: "application/json",
        buffer: Buffer.from(JSON.stringify(conflictingConfiguration)),
      });
      const conflictPreviewed = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "import_preview",
      );
      await portableSection
        .getByRole("button", { name: "Preview configuration import", exact: true })
        .click();
      const conflictPreview = await (await conflictPreviewed).json();
      assert.equal(conflictPreview.preview.conflicts[0].key, "profile:work");
      assert.equal(
        await portableSection
          .getByRole("button", { name: "Apply reviewed configuration", exact: true })
          .isDisabled(),
        true,
      );
      await portableSection.getByRole("button", { name: "Cancel preview", exact: true }).click();
      await portableSection
        .getByText("Preview cancelled. Local state is unchanged.", { exact: true })
        .waitFor();

      const keepLocalPreviewed = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "import_preview",
      );
      await portableSection
        .getByRole("button", { name: "Preview configuration import", exact: true })
        .click();
      await keepLocalPreviewed;
      await portableSection
        .getByRole("combobox", { name: "Resolution for profile:work", exact: true })
        .selectOption("keep_local");
      await portableSection
        .getByRole("combobox", { name: "Resolution for preferences:operational", exact: true })
        .selectOption("keep_local");
      const keptLocalConfiguration = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "import_apply",
      );
      await portableSection
        .getByRole("button", { name: "Apply reviewed configuration", exact: true })
        .click();
      assert.equal((await keptLocalConfiguration).status(), 200);
      await page.waitForFunction(() => document.documentElement.dataset.theme === "light");
      const keptLocalExport = await page.request.get(new URL("/api/v1/configuration", link).href);
      assert.equal(
        (await keptLocalExport.json()).preview.bundle.operational_preferences.appearance,
        "light",
      );
      await portableSection
        .getByText(
          "Configuration applied. Imported profiles remain Pending until you choose a local Identity Home and authenticate through Codex.",
          { exact: true },
        )
        .waitFor();

      await configurationFile.setInputFiles({
        name: "replace-preferences-configuration.json",
        mimeType: "application/json",
        buffer: Buffer.from(JSON.stringify(conflictingConfiguration)),
      });
      const replacementPreviewed = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "import_preview",
      );
      await portableSection
        .getByRole("button", { name: "Preview configuration import", exact: true })
        .click();
      assert.equal((await replacementPreviewed).status(), 200);
      await portableSection
        .getByRole("combobox", { name: "Resolution for profile:work", exact: true })
        .selectOption("keep_local");
      await portableSection
        .getByRole("combobox", { name: "Resolution for preferences:operational", exact: true })
        .selectOption("use_imported");
      const replacementApplied = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "import_apply",
      );
      await portableSection
        .getByRole("button", { name: "Apply reviewed configuration", exact: true })
        .click();
      assert.equal((await replacementApplied).status(), 200);
      await page.waitForFunction(() => document.documentElement.dataset.theme === "dark");
      const replacementExport = await page.request.get(new URL("/api/v1/configuration", link).href);
      const replacedBundle = (await replacementExport.json()).preview.bundle;
      assert.equal(replacedBundle.operational_preferences.appearance, "dark");
      assert.equal(
        replacedBundle.profiles.find((profile) => profile.alias === "Work").display_name,
        "Work",
      );
      check(
        "portable configuration can explicitly replace imported preferences while preserving a keep-local profile",
      );
      await page.getByRole("combobox", { name: "Appearance", exact: true }).selectOption("light");
      await page.waitForFunction(() => document.documentElement.dataset.theme === "light");

      await configurationFile.setInputFiles({
        name: "new-device-configuration.json",
        mimeType: "application/json",
        buffer: Buffer.from(
          JSON.stringify({
            schema_version: 1,
            profiles: [{ alias: "Imported", display_name: "Imported Profile" }],
            configuration_packs: [],
            alert_thresholds: [],
            operational_preferences: configurationPreview.preview.bundle.operational_preferences,
          }),
        ),
      });
      const importPreviewed = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "import_preview",
      );
      await portableSection
        .getByRole("button", { name: "Preview configuration import", exact: true })
        .click();
      const importedPreview = await (await importPreviewed).json();
      assert.deepEqual(importedPreview.preview.conflicts, []);
      const configurationApplied = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/configuration") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "import_apply",
      );
      await portableSection
        .getByRole("button", { name: "Apply reviewed configuration", exact: true })
        .click();
      assert.equal((await configurationApplied).status(), 200);
      await portableSection
        .getByText(
          "Configuration applied. Imported profiles remain Pending until you choose a local Identity Home and authenticate through Codex.",
          { exact: true },
        )
        .waitFor();
      const importedProfiles = await page.request.get(new URL("/api/v1/profiles", link).href);
      const importedProfile = (await importedProfiles.json()).profiles.find(
        (profile) => profile.alias === "Imported",
      );
      assert.equal(importedProfile.status, "pending");
      assert.equal(importedProfile.selected, false);
      await scanAccessibility("portable-configuration-settings");
      check(
        "portable configuration previews exclusions and conflicts, cancels without mutation, and imports a new profile as Pending",
      );

      const automaticUpdates = page.getByLabel("Check automatically", { exact: true });
      assert.equal(await automaticUpdates.isChecked(), false);
      assert.match(
        await page.locator("main").innerText(),
        /Updates[\s\S]*Automatic checks are off[\s\S]*Check for updates/,
      );
      const updateChecked = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/updates") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "check",
      );
      await page.getByRole("button", { name: "Check for updates", exact: true }).click();
      const updatePayload = await (await updateChecked).json();
      assert.equal(updatePayload.status, "update_available");
      assert.equal(updatePayload.automatic_checks, false);
      assert.match(updatePayload.download_url, /\/downloads\/0\.0\.2-alpha\/$/);
      assert.match(
        await page.locator("main").innerText(),
        /A newer CodexFolio version is available[\s\S]*0\.0\.2-alpha[\s\S]*Recording fixture release notes[\s\S]*Verified download location[\s\S]*Download and run the platform installer/,
      );
      for (const [mode, expectedStatus, expectedCopy] of [
        ["update-up-to-date", "up_to_date", "This CodexFolio version is up to date."],
        [
          "update-malformed",
          "malformed",
          "The update service returned invalid evidence. No download location is shown.",
        ],
        [
          "update-offline",
          "offline",
          "The update service could not be reached. Local features remain available.",
        ],
        [
          "update-unavailable",
          "unavailable",
          "The update service is unavailable. Local features remain available.",
        ],
      ]) {
        writeFileSync(control, mode);
        const checked = page.waitForResponse(
          (response) =>
            response.url().endsWith("/api/v1/updates") &&
            response.request().method() === "POST" &&
            response.request().postDataJSON().action === "check",
        );
        await page.getByRole("button", { name: "Check for updates", exact: true }).click();
        const response = await checked;
        assert.equal(response.status(), 200);
        assert.equal((await response.json()).status, expectedStatus);
        await page.getByText(expectedCopy, { exact: true }).waitFor();
        assert.equal(await automaticUpdates.isChecked(), false);
      }
      writeFileSync(control, "supported");
      check("Update checks isolate up-to-date and fixture failure states without changing consent");
      await automaticUpdates.check();
      const updatesEnabled = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/updates") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "configure" &&
          response.request().postDataJSON().automatic_checks === true,
      );
      await page
        .getByRole("button", { name: "Save automatic-check preference", exact: true })
        .click();
      assert.equal((await updatesEnabled).status(), 200);
      const persistedUpdates = await page.request.get(new URL("/api/v1/updates", link).href);
      assert.equal(persistedUpdates.status(), 200);
      assert.equal((await persistedUpdates.json()).automatic_checks, true);
      await automaticUpdates.uncheck();
      const updatesRevoked = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/updates") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "configure" &&
          response.request().postDataJSON().automatic_checks === false,
      );
      await page
        .getByRole("button", { name: "Save automatic-check preference", exact: true })
        .click();
      assert.equal((await updatesRevoked).status(), 200);
      check("Settings keeps explicit and automatic update consent separate and revocable");
      assert.match(
        await page.locator("main").innerText(),
        /Optional telemetry[\s\S]*Off · Awaiting explicit consent/,
      );
      await page.getByText("Inspect public schema and prerequisites", { exact: true }).click();
      assert.match(
        await page.locator("main").innerText(),
        /Public event schema[\s\S]*schema_version[\s\S]*Always excluded[\s\S]*credentials[\s\S]*30-day event deletion job/,
      );
      const telemetryEnabled = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/telemetry") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "enable",
      );
      await page.getByRole("button", { name: "Consent and enable schema v1", exact: true }).click();
      assert.equal((await telemetryEnabled).status(), 200);
      const telemetryStatus = await page.request.get(new URL("/api/v1/telemetry", link).href);
      const enabledTelemetry = await telemetryStatus.json();
      assert.equal(enabledTelemetry.enabled, true);
      assert.equal(enabledTelemetry.installation_id_present, true);
      assert.equal("installation_id" in enabledTelemetry, false);
      const telemetryReset = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/telemetry") &&
          response.request().postDataJSON().action === "reset_id",
      );
      await page.getByRole("button", { name: "Reset installation ID", exact: true }).click();
      assert.equal((await telemetryReset).status(), 200);
      const telemetryRevoked = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/telemetry") &&
          response.request().postDataJSON().action === "revoke",
      );
      await page.getByRole("button", { name: "Revoke consent", exact: true }).click();
      assert.equal((await telemetryRevoked).status(), 200);
      await page.getByText("Telemetry consent revoked immediately.", { exact: true }).waitFor();
      check(
        "Settings requires schema-versioned telemetry consent and supports immediate reset and revoke without exposing the installation ID",
      );
      await page.getByText("Inspect public schema and prerequisites", { exact: true }).click();
      assert.match(
        await page.locator("main").innerText(),
        /Local diagnostics[\s\S]*Enabled · 14 days · 50 MB/,
      );
      const diagnosticSettings = page.locator('section[aria-labelledby="diagnostics-title"]');
      await diagnosticSettings.getByLabel("Collect local diagnostics", { exact: true }).uncheck();
      await diagnosticSettings.locator("select").selectOption("warning");
      await diagnosticSettings.locator('input[type="number"]').fill("7");
      const diagnosticsSaved = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/diagnostics") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "configure",
      );
      await page.getByRole("button", { name: "Save diagnostic settings", exact: true }).click();
      assert.equal((await diagnosticsSaved).status(), 200);
      await page
        .getByText("Diagnostic settings saved. Any earlier preview was discarded.", {
          exact: true,
        })
        .waitFor();
      const diagnosticsPreviewed = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/diagnostics") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "preview",
      );
      await page.getByRole("button", { name: "Preview diagnostic export", exact: true }).click();
      const previewPayload = await (await diagnosticsPreviewed).json();
      assert.equal(previewPayload.settings.enabled, false);
      assert.equal(previewPayload.settings.minimum_level, "warning");
      assert.equal(previewPayload.settings.retention_days, 7);
      assert.ok(previewPayload.preview.confirmation_digest);
      assert.match(
        await page.locator("main").innerText(),
        /Diagnostic export preview[\s\S]*Excluded: identities/,
      );
      await page.getByRole("button", { name: "Cancel preview", exact: true }).click();
      assert.equal(
        await page.getByRole("heading", { name: "Diagnostic export preview" }).count(),
        0,
      );
      const diagnosticsPreviewedAgain = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/diagnostics") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "preview",
      );
      await page.getByRole("button", { name: "Preview diagnostic export", exact: true }).click();
      const secondPreviewPayload = await (await diagnosticsPreviewedAgain).json();
      await page.getByRole("heading", { name: "Diagnostic export preview" }).waitFor();
      const diagnosticsExported = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/diagnostics") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON().action === "export",
      );
      const diagnosticDownload = page.waitForEvent("download");
      await page.getByRole("button", { name: "Download reviewed JSON", exact: true }).click();
      assert.equal((await diagnosticsExported).status(), 200);
      const downloadedDiagnostics = await diagnosticDownload;
      assert.equal(downloadedDiagnostics.suggestedFilename(), "codex-folio-diagnostics.json");
      const diagnosticBody = readFileSync(await downloadedDiagnostics.path(), "utf8");
      assert.doesNotMatch(diagnosticBody, /Work|repository|arguments|analytics|checkpoint/);
      assert.match(diagnosticBody, /"schema_version": 1/);
      assert.deepEqual(JSON.parse(diagnosticBody), secondPreviewPayload.preview.bundle);
      await scanAccessibility("diagnostics-settings");
      check("Settings persists independent diagnostic controls and previews before local download");
      await page.getByText("Checkpoint inventory loaded.", { exact: true }).waitFor();
      const checkpointSection = page.locator('section[aria-labelledby="checkpoint-data-title"]');
      const inventoryBeforeRefresh = await checkpointSection.locator("article").allTextContents();
      const refreshedInventory = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/handoff") &&
          response.request().postDataJSON().action === "list",
      );
      await page.getByRole("button", { name: "Refresh inventory", exact: true }).click();
      assert.equal((await refreshedInventory).status(), 200);
      await page.waitForFunction(() =>
        [...document.querySelectorAll("button")].some(
          (button) => button.textContent?.trim() === "Refresh inventory" && !button.disabled,
        ),
      );
      assert.deepEqual(
        await checkpointSection.locator("article").allTextContents(),
        inventoryBeforeRefresh,
      );
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
      const assistedCheckpoint = checkpointSection
        .locator("article")
        .filter({ hasText: approved.revision.slice(0, 12) })
        .filter({ hasText: "Completed" });
      await assistedCheckpoint.getByRole("button", { name: "Preview export", exact: true }).click();
      await page
        .getByLabel("Export passphrase", { exact: true })
        .fill("cancelled-fixture-passphrase");
      await page.getByRole("button", { name: "Cancel export", exact: true }).click();
      assert.equal(
        await page.getByRole("button", { name: "Download export", exact: true }).count(),
        0,
      );
      const inventoryAfterCancellation = await checkpointSection
        .locator("article")
        .allTextContents();
      await assistedCheckpoint.getByRole("button", { name: "Preview export", exact: true }).click();
      assert.equal(await page.getByLabel("Export passphrase", { exact: true }).inputValue(), "");
      assert.deepEqual(
        await checkpointSection.locator("article").allTextContents(),
        inventoryAfterCancellation,
      );
      check(
        "checkpoint inventory refresh preserves records; export cancellation clears passphrase and preview",
      );
      await page
        .getByLabel("Export · Atlas")
        .getByText(/Always excluded/)
        .waitFor();
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
      await assistedCheckpoint.getByRole("button", { name: "Preview export", exact: true }).click();
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
      await page
        .getByRole("navigation", { name: "Settings sections", exact: true })
        .getByRole("link", { name: "Appearance", exact: true })
        .click();
      assert.equal(
        await page
          .getByRole("heading", { name: "Appearance", exact: true })
          .evaluate((element) => element === document.activeElement),
        true,
      );
      check("Settings section navigation moves keyboard focus directly to Appearance");
      await testSettingsNarrowReflow(page, check);
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

      await page.getByRole("button", { name: "Imported Profile", exact: true }).click();
      await page.getByRole("button", { name: "Pending · Resume setup", exact: true }).click();
      await assertFocusedHeading("Finish setting up Imported Profile");
      assert.equal(await page.getByLabel("CLI Alias", { exact: true }).inputValue(), "Imported");
      assert.equal(
        await page.getByLabel("Managed Identity Home (default)", { exact: true }).isChecked(),
        true,
      );
      await profileAction("Continue in Codex");
      await page.getByText("Waiting for terminal setup to start…", { exact: true }).waitFor();
      assert.match(await page.locator("main").innerText(), /profile add Imported --browser/);
      await runTerminalProfile("Imported");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();
      const importedHistoryOffer = page.getByRole("region", { name: "Import existing history?" });
      await importedHistoryOffer
        .getByRole("heading", { name: "Review local history sources" })
        .waitFor();
      await importedHistoryOffer
        .getByText(/candidate sessions/)
        .first()
        .waitFor();
      assert.equal(
        await importedHistoryOffer
          .getByRole("button", { name: "Import source" })
          .first()
          .isDisabled(),
        true,
      );
      await scanAccessibility("onboarding-history");
      await capture("onboarding-history-narrow", 390, 844);
      await importedHistoryOffer.getByRole("button", { name: "Not now" }).focus();
      await page.keyboard.press("Enter");
      assert.equal(await importedHistoryOffer.count(), 0);
      await page.setViewportSize({ width: 1440, height: 1000 });
      assert.doesNotMatch(await page.locator("main").innerText(), /browser-auth-secret/);
      check(
        "an imported profile requires an explicit local Identity Home choice and fake-Codex authentication before becoming Ready",
      );

      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await assertFocusedHeading("Add Identity Profile");
      await scanAccessibility("profile-setup");
      await page.getByLabel("Display Name", { exact: true }).fill("Research");
      await page.getByLabel("CLI Alias", { exact: true }).fill("Research");
      await profileAction("Save and close");
      await page.getByText("Pending · Resume setup", { exact: true }).first().waitFor();
      await page.getByRole("button", { name: "Pending · Resume setup", exact: true }).click();
      await profileAction("Continue in Codex");
      await page.getByText("Waiting for terminal setup to start…", { exact: true }).waitFor();
      await runTerminalProfile("Research");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();
      const researchHistoryOffer = page.getByRole("region", { name: "Import existing history?" });
      await researchHistoryOffer
        .getByText(/candidate sessions/)
        .first()
        .waitFor();
      const researchSource = researchHistoryOffer.getByRole("region", {
        name: "Work",
        exact: true,
      });
      await researchSource
        .getByRole("checkbox", { name: /I choose to import this source/ })
        .check();
      const onboardingImport = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/activity/sources") &&
          response.request().method() === "POST",
      );
      await researchSource.getByRole("button", { name: "Import source" }).click();
      assert.equal((await onboardingImport).status(), 200);
      await researchHistoryOffer.getByText(/existing sessions were skipped/).waitFor();
      await researchHistoryOffer.getByRole("button", { name: "Not now" }).click();
      assert.doesNotMatch(await page.locator("main").innerText(), /browser-auth-secret/);
      check(
        "profile completion offers reviewed history with separate consent, decline and repeat import",
      );

      await page.getByRole("button", { name: "Work", exact: true }).click();
      const profileLaunchBefore = await (
        await page.request.get(new URL("/api/v1/activity", link).href)
      ).json();
      await page.getByRole("button", { name: "Launch Codex", exact: true }).click();
      await assertFocusedHeading("Launch prepared in your terminal");
      assert.match(await page.locator("main").innerText(), /Launch Profile[\s\S]*Work/);
      await page.getByRole("button", { name: "Back", exact: true }).click();
      await assertFocusedHeading("Profiles");
      assert.deepEqual(
        await (await page.request.get(new URL("/api/v1/activity", link).href)).json(),
        profileLaunchBefore,
      );
      check("Profiles Launch entry prepares the chosen profile and Back starts no process");
      await page.getByRole("button", { name: "Manage Shared Configuration", exact: true }).click();
      await assertFocusedHeading("Shared Configuration Packs");
      assert.doesNotMatch(await page.locator("main").innerText(), /model =|gpt-5-mini/);
      await page.getByText("Create a declarative draft", { exact: true }).click();
      const draftDocuments = [
        { kind: "config", label: "config.toml (optional)", content: 'model = "gpt-5-mini"\n' },
        {
          kind: "agents",
          label: "AGENTS.md (optional)",
          content: "Review repository changes before continuing.\n",
        },
        { kind: "plugins", label: "plugins.lock (optional)", content: "version = 1\n" },
      ];
      assert.equal(
        await page.getByRole("button", { name: "Save draft", exact: true }).isDisabled(),
        true,
      );
      await page.getByLabel("Pack ID", { exact: true }).fill("browser-reviewed-draft");
      await page.getByLabel("Version", { exact: true }).fill("1");
      for (const item of draftDocuments)
        await page.getByLabel(item.label, { exact: true }).fill(item.content);
      const createdDraftResponse = await configurationAction("Save draft", "create");
      assert.equal(createdDraftResponse.status(), 200, await createdDraftResponse.text());
      const createdDraft = (await createdDraftResponse.json()).pack;
      assert.equal(createdDraft.state, "draft");
      assert.deepEqual(
        createdDraftResponse.request().postDataJSON().documents,
        draftDocuments.map(({ kind, content }) => ({ kind, content })),
      );
      const draftItem = page.locator("li").filter({ hasText: "browser-reviewed-draft · 1" });
      await draftItem.getByText(createdDraft.digest, { exact: true }).waitFor();
      for (const item of draftDocuments) {
        const disclosure = draftItem
          .locator("details")
          .filter({ has: page.locator("summary", { hasText: item.kind }) });
        await disclosure.locator("summary").focus();
        await page.keyboard.press("Enter");
        assert.equal(await disclosure.locator("pre").isVisible(), true);
        assert.equal(await disclosure.locator("pre").textContent(), item.content);
      }
      const draftApproval = await configurationAction("Approve reviewed draft", "approve");
      assert.equal(draftApproval.status(), 200, await draftApproval.text());
      assert.equal((await draftApproval.json()).pack.state, "approved");
      await page.getByText("Reviewed pack version approved.", { exact: true }).waitFor();
      assert.equal(
        await draftItem
          .getByRole("button", { name: "Approve reviewed draft", exact: true })
          .count(),
        0,
      );
      const approvedPackChoices = page.getByRole("combobox", {
        name: "Approved pack version",
        exact: true,
      });
      assert.equal(
        await approvedPackChoices.locator('option[value="browser-reviewed-draft@1"]').count(),
        1,
      );
      check(
        "pack draft creation reviews every exact document by keyboard before explicit approval",
      );
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

      const unsignedReferencedHome = mkdtempSync(join(output, "referenced-awaiting-auth-"));
      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await page.getByLabel("Display Name", { exact: true }).fill("Referenced pending");
      await page.getByLabel("CLI Alias", { exact: true }).fill("ReferencePending");
      await page.getByLabel("Reference an existing Identity Home", { exact: true }).check();
      await page
        .getByLabel("Existing Identity Home path", { exact: true })
        .fill(unsignedReferencedHome);
      writeFileSync(control, "profile-needs-auth");
      await profileAction("Use existing sign-in");
      await page.getByText("Waiting for terminal setup to start…", { exact: true }).waitFor();
      assert.match(
        await page.locator("main").innerText(),
        /profile add ReferencePending --browser/,
      );
      assert.doesNotMatch(
        await page.locator("main").innerText(),
        new RegExp(unsignedReferencedHome),
      );
      await runTerminalProfile("ReferencePending");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();

      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await page.getByLabel("Display Name", { exact: true }).fill("Referenced");
      await page.getByLabel("CLI Alias", { exact: true }).fill("Referenced");
      await page.getByLabel("Reference an existing Identity Home", { exact: true }).check();
      await page.getByLabel("Existing Identity Home path", { exact: true }).fill(referencedHome);
      const referencedSessionId = "018f4f70-6f77-7c3f-9b77-93aa087dfc54";
      const referencedDatabase = new DatabaseSync(join(referencedHome, "state_5.sqlite"));
      try {
        const fixture = readFileSync(
          join(
            "..",
            "..",
            "internal",
            "adapters",
            "codex",
            "testdata",
            "local-state",
            "v5",
            "threads.sql",
          ),
          "utf8",
        );
        referencedDatabase.exec(fixture.slice(0, fixture.indexOf("INSERT INTO threads")));
        referencedDatabase
          .prepare(
            "INSERT INTO threads (id, created_at_ms, updated_at_ms, source, model, cwd, tokens_used, title, preview, first_user_message) VALUES (?, ?, ?, 'cli', 'gpt-5', ?, 913, 'private title', 'private preview', 'private prompt')",
          )
          .run(referencedSessionId, Date.now(), Date.now(), referencedHome);
      } finally {
        referencedDatabase.close();
      }
      const reusedSignIn = await profileAction("Use existing sign-in");
      assert.equal(reusedSignIn.request().postDataJSON().action, "prepare");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();
      assert.doesNotMatch(await page.locator("main").innerText(), new RegExp(referencedHome));
      const referencedHistoryOffer = page.getByRole("region", { name: "Import existing history?" });
      const referencedSource = referencedHistoryOffer.getByRole("region", {
        name: "Referenced",
        exact: true,
      });
      await referencedSource.getByText("1 candidate sessions").waitFor();
      assert.equal(
        await referencedHistoryOffer
          .getByRole("button", { name: "Continue to launch" })
          .isEnabled(),
        true,
      );
      await referencedSource
        .getByRole("checkbox", { name: /I choose to import this source/ })
        .check();
      const beforeReferencedImport = (
        await (await page.request.get(new URL("/api/v1/activity", link).href)).json()
      ).records;
      await page.route("**/api/v1/activity/sources", async (route) => {
        if (route.request().method() === "POST")
          await route.fulfill({ status: 503, body: "fixture unavailable" });
        else await route.continue();
      });
      await referencedSource.getByRole("button", { name: "Import source" }).click();
      await referencedHistoryOffer
        .getByText("Import failed. No success is assumed. Review the source and try again.")
        .waitFor();
      assert.equal(
        await referencedHistoryOffer
          .getByRole("button", { name: "Continue to launch" })
          .isEnabled(),
        true,
      );
      assert.deepEqual(
        (await (await page.request.get(new URL("/api/v1/activity", link).href)).json()).records,
        beforeReferencedImport,
      );
      const referencedProfile = (
        await (await page.request.get(new URL("/api/v1/profiles", link).href)).json()
      ).profiles.find((profile) => profile.alias === "Referenced");
      assert.equal(referencedProfile.status, "ready");
      await page.unroute("**/api/v1/activity/sources");
      const referencedImport = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/activity/sources") &&
          response.request().method() === "POST",
      );
      await referencedSource.getByRole("button", { name: "Import source" }).click();
      assert.equal((await referencedImport).status(), 200);
      await referencedHistoryOffer.getByText(/1 new sessions were added/).waitFor();
      const afterReferencedImport = (
        await (await page.request.get(new URL("/api/v1/activity", link).href)).json()
      ).records;
      assert.equal(afterReferencedImport.length, beforeReferencedImport.length + 1);
      assert.equal(
        afterReferencedImport.filter((record) => record.source_session_id === referencedSessionId)
          .length,
        1,
      );
      await referencedHistoryOffer.getByRole("button", { name: "Not now" }).click();
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Sessions", exact: true })
        .click();
      await page
        .getByRole("combobox", { name: "Record type", exact: true })
        .selectOption("observed_session");
      await page
        .getByRole("combobox", { name: "Profile", exact: true })
        .selectOption("__unassigned__");
      await page.getByRole("combobox", { name: "Date range", exact: true }).selectOption("all");
      await page
        .getByRole("table", { name: "Metadata timeline" })
        .locator("tbody tr")
        .first()
        .getByRole("button", { name: "Open details" })
        .click();
      assert.match(await page.locator("main").innerText(), /Observed Session · Unassigned History/);
      assert.match(await page.locator("main").innerText(), new RegExp(referencedSessionId));
      await page.getByRole("button", { name: "Back to Sessions", exact: true }).click();
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Profiles", exact: true })
        .click();
      check(
        "referenced-home onboarding import adds a new Unassigned session to history; failure keeps Ready launch available",
      );

      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await page.getByLabel("Display Name", { exact: true }).fill("Device");
      await page.getByLabel("CLI Alias", { exact: true }).fill("Device");
      await page.getByLabel("Device code", { exact: true }).check();
      await profileAction("Continue in Codex");
      await page
        .getByText(
          "Run this command in your terminal. This page will update when Codex finishes:",
          { exact: true },
        )
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
      assert.equal((await failed).status(), 200);
      await runTerminalProfile("Failure", "browser", "CF_PROFILE_AUTHENTICATION_FAILED");
      await page.getByText("Codex authentication did not complete.", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      const failedProfile = page.getByRole("button", { name: "Failure", exact: true });
      await failedProfile.waitFor();
      await failedProfile.click();
      assert.match(await page.locator("main").innerText(), /Pending · Resume setup/);

      writeFileSync(control, "profile-auth-cancel");
      await page.getByRole("button", { name: "Add Identity Profile", exact: true }).click();
      await page.getByLabel("Display Name", { exact: true }).fill("Interrupted");
      await page.getByLabel("CLI Alias", { exact: true }).fill("Interrupted");
      await profileAction("Continue in Codex");
      await runTerminalProfile("Interrupted", "browser", "CF_PROFILE_AUTHENTICATION_CANCELLED");
      await page.getByText("Codex authentication was cancelled.", { exact: true }).waitFor();
      await page.getByRole("button", { name: "Cancel", exact: true }).click();
      await page.getByRole("button", { name: "Interrupted", exact: true }).click();
      await page.getByRole("button", { name: "Pending · Resume setup", exact: true }).click();
      writeFileSync(control, "supported");
      await profileAction("Continue in Codex");
      await runTerminalProfile("Interrupted");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();
      check(
        "dashboard terminal handoff observes completion, failure and interrupted Pending resume without auth output",
      );

      await page.getByRole("button", { name: "Work", exact: true }).click();
      await testNarrowFocusVisibility(page, check);
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
      await runTerminalProfile("Work", "browser", "", "reauthenticate");
      await page.getByText("Identity Profile is ready.", { exact: true }).waitFor();
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
              .getByRole("heading", { name: "Weekly window", exact: true })
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
      assert.equal(await scope().inputValue(), "Work");
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
      await page.reload();
      await page
        .getByText("Open a one-time dashboard link from your terminal.", { exact: true })
        .waitFor();
      assert.equal(
        await page.getByRole("heading", { name: "Current capacity", exact: true }).count(),
        0,
      );
      check("reloading the stripped dashboard URL requires explicit one-time terminal re-entry");
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
    if (phase === "smoke") {
      await page
        .getByRole("navigation", { name: "Primary", exact: true })
        .getByRole("button", { name: "Settings", exact: true })
        .click();
      await page.getByRole("button", { name: "Forget this browser" }).click();
      await page.getByRole("heading", { name: "Relaunch CodexFolio", exact: true }).waitFor();
      await page.reload();
      await page.getByText("Open a one-time dashboard link from your terminal.").waitFor();
      check("forgetting the browser ends the session and prevents reopening");
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
