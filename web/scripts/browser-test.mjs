/* global document, window, innerWidth, getComputedStyle */
import { URL } from "node:url";
import { performance } from "node:perf_hooks";
import assert from "node:assert/strict";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { cpus, release, totalmem, tmpdir } from "node:os";
import { chromium } from "playwright";
import axe from "axe-core";

const [link, control, phase = "deep", referencedHome] = process.argv.slice(2);
assert.ok(["deep", "smoke", "benchmark", "reentry"].includes(phase), "unknown browser phase");
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
page.setDefaultTimeout(10000);
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
  await scope().selectOption(value);
  if (changed) await changed;
  await page.waitForFunction(
    (expected) =>
      [...document.querySelectorAll('select[aria-label="Dashboard Scope"]')].every(
        (select) => select.value === expected && !select.disabled,
      ),
    value,
  );
  if (changed && confirm)
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
  if (phase === "benchmark") {
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
    assert.match(await page.locator("main").innerText(), /18 minutes ago/);
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
      assert.ok(
        (await page.locator("main").innerText()).includes(
          `codex-folio launch Personal --project ${projectId} --`,
        ),
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
      await page.getByRole("button", { name: "Prepare Handoff", exact: true }).click();
      assert.match(await page.locator("main").innerText(), /Nothing has been prepared or started/);
      await page.getByRole("button", { name: "Close", exact: true }).click();
      await page.getByRole("button", { name: "Open details", exact: true }).click();
      assert.match(await page.locator("main").innerText(), /codex.primary.used_percent/);
      await page.getByRole("button", { name: "Open details", exact: true }).click();
      check("foreground terminal handoff, cancellation, lifecycle, nonzero exit and failed start");
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
      await page.getByRole("button", { name: "Failure", exact: true }).click();
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
      const quarantine = await lifecycleAction("Confirm local removal", "remove");
      assert.equal(quarantine.status(), 200, await quarantine.text());
      await page
        .getByText("Identity Profile moved to seven-day local quarantine.", { exact: true })
        .waitFor();
      assert.match(await page.locator("main").innerText(), /Recoverable until/);
      await lifecycleAction("Restore", "restore");
      await page
        .getByText("Identity Profile restored from local quarantine.", { exact: true })
        .waitFor();

      await page.getByRole("button", { name: "Research", exact: true }).click();
      await page.getByText("Removal and recovery", { exact: true }).click();
      await lifecycleAction("Review local removal", "preview");
      await page.getByLabel("Type CLI Alias to confirm", { exact: true }).fill("Research");
      await lifecycleAction("Confirm local removal", "remove");
      await page
        .getByText("Identity Profile moved to seven-day local quarantine.", { exact: true })
        .waitFor();
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
