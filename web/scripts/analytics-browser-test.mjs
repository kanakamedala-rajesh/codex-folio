/* global document, requestAnimationFrame, setTimeout, window */
import { URL } from "node:url";
import { performance } from "node:perf_hooks";
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";

export async function testAnalytics({
  page,
  link,
  control,
  capture,
  scanAccessibility,
  check,
  results,
  axeSource,
}) {
  async function waitForControl(expected, failed) {
    for (let attempt = 0; attempt < 500; attempt++) {
      const state = readFileSync(control, "utf8");
      if (state === expected) return;
      assert.notEqual(state, failed);
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
    assert.equal(readFileSync(control, "utf8"), expected);
  }
  writeFileSync(control, "analytics-seed");
  await waitForControl("analytics-seeded", "analytics-seed-failed");
  const selectedBefore = await (
    await page.request.get(new URL("/api/v1/selection", link).href)
  ).json();
  const responsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/analytics/history") &&
      response.request().method() === "POST",
  );
  const started = performance.now();
  const analyticsLink = page
    .getByRole("navigation", { name: "Primary", exact: true })
    .getByRole("button", { name: "Analytics", exact: true });
  await analyticsLink.focus();
  await analyticsLink.press("Enter");
  const response = await responsePromise;
  assert.equal(response.status(), 200, await response.text());
  const payload = await response.text();
  for (const excluded of [
    "analytics-shared-login@example.test",
    "analytics-private-workspace",
    "identity_home",
    "credential",
    "command sentinel",
  ])
    assert.ok(!payload.includes(excluded), excluded);
  assert.doesNotMatch(payload, /"(?:login_identity|workspace|canonical_path|identity_home)"/);
  const request = response.request().postDataJSON();
  assert.equal(request.action, "aggregates");
  assert.deepEqual(request.scope.classes, ["aggregates"]);
  assert.equal(request.scope.project_id, "*");
  await page.getByText(/compatible stored aggregate records loaded\./).waitFor();
  const chart = page.getByRole("img", { name: /Monthly final capacity samples/ });
  await chart.waitFor();
  assert.ok(await chart.locator('path[stroke="var(--cyan)"][d*="L"]').count());
  assert.ok(await chart.locator('path[stroke="var(--magenta)"][d*="L"]').count());
  const table = page.getByRole("table", {
    name: /The same monthly final samples as the chart/,
  });
  assert.equal(await table.locator("tbody tr").count(), 13);
  await page.evaluate(
    () => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))),
  );
  results.analytics13MonthMs = performance.now() - started;
  const tableText = await table.innerText();
  assert.match(tableText, /Unavailable/);
  assert.match(tableText, /Provider reported/);
  assert.match(tableText, /Locally derived/);
  assert.match(tableText, /Estimated/);
  assert.match(tableText, /Assumptions bounded fixture estimate/);
  assert.match(tableText, /Uncertainty not provider reported/);
  assert.match(tableText, /Last-known · stale/i);
  assert.match(tableText, /Contradictory/i);
  assert.match(tableText, /codex app server 0\.153\.4/i);
  assert.match(tableText, /Observed[\s\S]*Captured[\s\S]*Window/);
  const conflictingRow = table.locator("tbody tr").filter({ hasText: "conflict-v1" });
  assert.equal(await conflictingRow.count(), 1);
  const conflictingText = await conflictingRow.innerText();
  assert.match(conflictingText, /Unavailable/);
  assert.match(
    conflictingText,
    /35 percent[\s\S]*codex app server 0\.153\.4[\s\S]*Provider reported/i,
  );
  assert.match(conflictingText, /77 percent[\s\S]*derived conflict-v1[\s\S]*Locally derived/i);
  const conflictingIndex = await conflictingRow.evaluate((row) =>
    Array.from(row.parentElement.children).indexOf(row),
  );
  const conflictingX = (
    42 +
    (conflictingIndex * 888) / ((await table.locator("tbody tr").count()) - 1)
  ).toFixed(1);
  const secondaryPaths = await chart
    .locator('path[stroke="var(--magenta)"]')
    .evaluateAll((paths) => paths.map((path) => path.getAttribute("d") ?? ""));
  const conflictingPoint = new RegExp(`(?:M|L)${conflictingX.replace(".", "\\.")} `);
  assert.ok(secondaryPaths.every((path) => !conflictingPoint.test(path)));
  assert.match(await page.locator("main").innerText(), /Percentages are never summed/);
  check(
    "13-month real-service history uses one filtered dataset for native SVG and semantic table, preserving gaps, provenance and units",
  );
  await page.evaluate((source) => window.eval(source), axeSource);
  await scanAccessibility("analytics-capacity");
  const cdp = await page.context().newCDPSession(page);
  const accessibilityTree = await cdp.send("Accessibility.getFullAXTree");
  assert.ok(accessibilityTree.nodes.some((node) => node.role?.value === "table"));
  assert.ok(accessibilityTree.nodes.some((node) => node.role?.value === "status"));
  await capture("analytics-capacity-wide", 1440, 1000);
  await capture("analytics-capacity-narrow", 390, 844);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.evaluate(() => {
    document.documentElement.dataset.theme = "light";
  });
  await capture("analytics-capacity-light", 1440, 1000);
  await page.evaluate(() => {
    delete document.documentElement.dataset.theme;
  });
  await page.emulateMedia({ colorScheme: "dark", reducedMotion: "reduce", forcedColors: "active" });
  await capture("analytics-capacity-forced", 390, 844);
  await page.emulateMedia({
    colorScheme: "dark",
    reducedMotion: "no-preference",
    forcedColors: "none",
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await capture("analytics-capacity-zoom-reflow", 720, 500);
  await page.setViewportSize({ width: 1440, height: 1000 });

  await page
    .getByRole("combobox", { name: "Capacity window", exact: true })
    .selectOption("codex.primary.used_percent");
  assert.equal(await chart.locator('path[stroke="var(--magenta)"]').count(), 0);
  await page.getByRole("combobox", { name: "Capacity window", exact: true }).selectOption("both");
  const reload = page.waitForResponse(
    (item) =>
      item.url().endsWith("/api/v1/analytics/history") && item.request().method() === "POST",
  );
  await page.getByRole("combobox", { name: "History range", exact: true }).selectOption("90-days");
  assert.equal((await reload).status(), 200);
  await page.getByText(/compatible stored aggregate records loaded\./).waitFor();
  check("history range and provider-window filters reload or refine compatible evidence");

  const compareButton = page.getByRole("button", { name: "Compare", exact: true });
  await compareButton.focus();
  await compareButton.press("Enter");
  await page.getByRole("heading", { name: "Compare capacity", exact: true }).waitFor();
  const selectedAfter = await (
    await page.request.get(new URL("/api/v1/selection", link).href)
  ).json();
  assert.equal(selectedAfter.profile_id, selectedBefore.profile_id);
  const compare = page.getByRole("table", {
    name: "Subscription capacity · Per-profile limits, never one pooled percentage",
  });
  assert.equal(await compare.locator("tbody tr").count(), 2);
  assert.match(await page.locator("main").innerText(), /Selected Profile remains/);
  assert.match(await page.locator("main").innerText(), /API credit and spend[\s\S]*Unavailable/);
  assert.doesNotMatch(await page.locator("main").innerText(), /pooled capacity|combined quota/i);
  check(
    "Compare is explicit, keeps Selected Profile unchanged, and separates per-profile subscription limits from API units",
  );

  await scanAccessibility("analytics-compare");
  await capture("analytics-compare-wide", 1440, 1000);
  await capture("analytics-compare-narrow", 390, 844);
  const firstDisclosure = page.locator('td[colspan="5"] summary').first();
  await firstDisclosure.focus();
  await firstDisclosure.press("Enter");
  assert.match(
    await page.locator('td[colspan="5"]').first().innerText(),
    /Secondary remaining[\s\S]*Evidence age \/ state[\s\S]*Source \/ provenance/,
  );
  assert.equal(
    await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth),
    false,
  );
  writeFileSync(control, "analytics-clean");
  await waitForControl("analytics-cleaned", "analytics-clean-failed");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page
    .getByRole("navigation", { name: "Primary", exact: true })
    .getByRole("button", { name: "Overview", exact: true })
    .click();
  await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
}
