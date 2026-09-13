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
  await page.route("**/api/v1/projects", async (route) => {
    await route.fulfill({ status: 500, contentType: "application/json", body: "{}" });
  });
  const failedProjects = page.waitForResponse(
    (item) => item.url().endsWith("/api/v1/projects") && item.request().method() === "GET",
  );
  const initialHistory = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/analytics/history") &&
      response.request().method() === "POST",
  );
  const analyticsLink = page
    .getByRole("navigation", { name: "Primary", exact: true })
    .getByRole("button", { name: "Analytics", exact: true });
  await analyticsLink.focus();
  await analyticsLink.press("Enter");
  assert.equal((await initialHistory).status(), 200);
  assert.equal((await failedProjects).status(), 500);
  await page.getByRole("button", { name: "Projects", exact: true }).click();
  await page
    .getByText("Project Identities could not be loaded. No Project Identity result is available.", {
      exact: true,
    })
    .first()
    .waitFor();
  assert.ok(
    await page.getByRole("status").filter({ hasText: "No Project Identity result" }).count(),
  );
  assert.doesNotMatch(await page.locator("main").innerText(), /No Project Identity matches/);
  await page.unroute("**/api/v1/projects");
  await page
    .getByRole("navigation", { name: "Primary", exact: true })
    .getByRole("button", { name: "Overview", exact: true })
    .click();
  const responsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/v1/analytics/history") &&
      response.request().method() === "POST",
  );
  const reloadedProjects = page.waitForResponse(
    (item) => item.url().endsWith("/api/v1/projects") && item.request().method() === "GET",
  );
  const started = performance.now();
  await analyticsLink.focus();
  await analyticsLink.press("Enter");
  assert.equal((await reloadedProjects).status(), 200);
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
  await page.setViewportSize({ width: 1440, height: 1000 });

  writeFileSync(control, "analytics-activity-seed");
  await waitForControl("analytics-activity-seeded", "analytics-activity-seed-failed");

  const tabCases = [
    ["Tokens", "Token observations", /42[\s\S]*Observed during session/],
    ["Models", "Models", /gpt-5[\s\S]*Local metadata[\s\S]*Observed during session/],
    ["Activity", "Activity", /Managed Launch[\s\S]*Observed Session/],
  ];
  for (const [tabName, heading, evidence] of tabCases) {
    const activityResponse = page.waitForResponse(
      (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
    );
    await page.getByRole("button", { name: tabName, exact: true }).click();
    const loaded = await activityResponse;
    assert.equal(loaded.status(), 200);
    assert.equal(new URL(loaded.url()).searchParams.get("profile"), "Work");
    assert.equal(new URL(loaded.url()).searchParams.has("project"), false);
    await page.getByRole("heading", { name: heading, exact: true }).first().waitFor();
    await page.getByText(/normalized activity records loaded\./).waitFor();
    assert.match(await page.locator("main").innerText(), evidence);
    if (tabName === "Tokens") assert.match(await page.getByRole("table").innerText(), /\b0\b/);
    assert.doesNotMatch(await page.locator("main").innerText(), /Managed Launch Launch/);
    assert.ok(await page.getByRole("img").count());
    assert.ok(await page.getByRole("table").count());
    await scanAccessibility(`analytics-${tabName.toLowerCase()}`);
    await capture(`analytics-${tabName.toLowerCase()}-wide`, 1440, 1000);
    await capture(`analytics-${tabName.toLowerCase()}-narrow`, 390, 844);
    if (tabName === "Models") {
      const modelDisclosure = page.getByRole("table").locator("summary").first();
      await modelDisclosure.focus();
      await modelDisclosure.press("Enter");
      assert.match(
        await modelDisclosure.locator("xpath=..").innerText(),
        /Records[\s\S]*Last observed[\s\S]*Evidence/,
      );
      assert.equal(
        await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth),
        false,
      );
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
  }

  const projectsResponse = page.waitForResponse(
    (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
  );
  await page.getByRole("button", { name: "Projects", exact: true }).click();
  assert.equal((await projectsResponse).status(), 200);
  await page.getByRole("heading", { name: "Projects", exact: true }).first().waitFor();
  assert.match(await page.locator("main").innerText(), /Atlas[\s\S]*Zephyr/);
  assert.match(await page.getByRole("table").innerText(), /Locally derived/);
  assert.match(await page.getByRole("table").innerText(), /Observed during session/);
  assert.doesNotMatch(
    await page.locator("main").innerText(),
    /\/tmp\/|canonical_path|identity_home/i,
  );
  await page.setViewportSize({ width: 390, height: 844 });
  const projectDisclosure = page.getByRole("table").locator("summary").first();
  assert.equal(await projectDisclosure.isVisible(), true);
  await projectDisclosure.focus();
  await projectDisclosure.press("Space");
  assert.equal(await projectDisclosure.evaluate((summary) => summary.parentElement.open), true);
  const editResponse = page.waitForResponse(
    (item) => item.url().endsWith("/api/v1/projects") && item.request().method() === "PUT",
  );
  await page.getByRole("button", { name: "Edit Project Alias", exact: true }).first().click();
  await page.getByRole("textbox", { name: "Project Alias", exact: true }).fill("Atlas Research");
  await page.getByRole("button", { name: "Save Alias", exact: true }).click();
  const edited = await editResponse;
  const editPayload = await edited.json();
  assert.equal(edited.status(), 200, JSON.stringify(editPayload));
  assert.deepEqual(edited.request().postDataJSON(), {
    project_id: requestProjectId(editPayload),
    alias: "Atlas Research",
  });
  assert.match(
    await page.locator("main").innerText(),
    /Atlas Research[\s\S]*Project Alias updated/,
  );
  await page.setViewportSize({ width: 640, height: 500 });
  await cdp.send("Emulation.setPageScaleFactor", { pageScaleFactor: 2 });
  const zoomMetrics = await page.evaluate(() => ({
    innerWidth: window.innerWidth,
    visualWidth: window.visualViewport?.width,
    visualScale: window.visualViewport?.scale,
    narrowLayout: window.matchMedia("(max-width: 767px)").matches,
    overflow: document.documentElement.scrollWidth > window.innerWidth,
  }));
  assert.deepEqual(zoomMetrics, {
    innerWidth: 640,
    visualWidth: 320,
    visualScale: 2,
    narrowLayout: true,
    overflow: false,
  });
  await projectDisclosure.focus();
  if (await projectDisclosure.evaluate((summary) => summary.parentElement.open)) {
    await projectDisclosure.press("Enter");
  }
  await projectDisclosure.press("Enter");
  assert.equal(await projectDisclosure.evaluate((summary) => summary.parentElement.open), true);
  const zoomedEdit = projectDisclosure.locator("xpath=../dl//button", {
    hasText: "Edit Project Alias",
  });
  await zoomedEdit.focus();
  assert.equal(await zoomedEdit.isVisible(), true);
  await cdp.send("Emulation.resetPageScaleFactor");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await scanAccessibility("analytics-projects");
  await capture("analytics-projects-wide", 1440, 1000);
  await capture("analytics-projects-narrow", 390, 844);
  await page.setViewportSize({ width: 1440, height: 1000 });

  const profileResponse = page.waitForResponse(
    (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
  );
  await page
    .getByRole("combobox", { name: "Analytics scope", exact: true })
    .selectOption({ label: "Personal" });
  const personal = await profileResponse;
  assert.equal(personal.status(), 200);
  assert.equal(new URL(personal.url()).searchParams.get("profile"), "Personal");
  const personalRecords = (await personal.json()).records;
  assert.ok(personalRecords.some((record) => record.tokens_used === "84"));
  assert.equal(
    await page.getByRole("combobox", { name: "History range", exact: true }).inputValue(),
    "90-days",
  );
  await page.getByText(/normalized activity records loaded\./).waitFor();
  assert.match(await page.getByRole("table").innerText(), /Atlas Research[\s\S]*Zephyr/);

  const ninetyDayTabs = [
    ["Tokens", "Token observations", 1, /84/],
    ["Projects", "Projects", 2, /Zephyr/],
    ["Models", "Models", 1, /gpt-5/],
    [
      "Activity",
      "Activity",
      personalRecords.length,
      new RegExp(`${personalRecords.length} matching records`),
    ],
  ];
  for (const [tabName, heading, rows, oldEvidence] of ninetyDayTabs) {
    const filteredResponse = page.waitForResponse(
      (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
    );
    await page.getByRole("button", { name: tabName, exact: true }).click();
    assert.equal((await filteredResponse).status(), 200);
    await page.getByRole("heading", { name: heading, exact: true }).first().waitFor();
    await page.locator("main").filter({ hasText: oldEvidence }).waitFor();
    assert.equal(await page.getByRole("table").locator("tbody tr").count(), rows, tabName);
    assert.match(await page.locator("main").innerText(), oldEvidence);
    if (tabName === "Projects") {
      const zephyrRow = page.getByRole("table").locator("tbody tr").filter({ hasText: "Zephyr" });
      assert.equal(await zephyrRow.locator("td").nth(2).innerText(), "1");
    }
  }

  const rangeResponse = page.waitForResponse(
    (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
  );
  await page.getByRole("combobox", { name: "History range", exact: true }).selectOption("30-days");
  const recent = await rangeResponse;
  assert.equal(recent.status(), 200);
  assert.equal(new URL(recent.url()).searchParams.get("profile"), "Personal");
  assert.equal(new URL(recent.url()).searchParams.has("project"), false);

  const thirtyDayTabs = [
    ["Tokens", "Token observations", null, /Token metrics are unsupported/, /84/],
    ["Projects", "Projects", 2, /Zephyr/, null],
    ["Models", "Models", null, /Model metadata is unsupported/, /gpt-5/],
    [
      "Activity",
      "Activity",
      personalRecords.length - 1,
      new RegExp(`${personalRecords.length - 1} matching records`),
      new RegExp(`${personalRecords.length} matching records`),
    ],
  ];
  for (const [tabName, heading, rows, expected, oldEvidence] of thirtyDayTabs) {
    const filteredResponse = page.waitForResponse(
      (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
    );
    await page.getByRole("button", { name: tabName, exact: true }).click();
    assert.equal((await filteredResponse).status(), 200);
    await page.getByRole("heading", { name: heading, exact: true }).first().waitFor();
    await page.locator("main").filter({ hasText: expected }).waitFor();
    if (rows === null) assert.equal(await page.getByRole("table").count(), 0);
    else assert.equal(await page.getByRole("table").locator("tbody tr").count(), rows, tabName);
    assert.match(await page.locator("main").innerText(), expected);
    if (oldEvidence) assert.doesNotMatch(await page.locator("main").innerText(), oldEvidence);
    if (tabName === "Projects") {
      const zephyrRow = page.getByRole("table").locator("tbody tr").filter({ hasText: "Zephyr" });
      assert.equal(await zephyrRow.locator("td").nth(2).innerText(), "0");
    }
  }

  const restoreRangeResponse = page.waitForResponse(
    (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
  );
  await page.getByRole("combobox", { name: "History range", exact: true }).selectOption("90-days");
  assert.equal((await restoreRangeResponse).status(), 200);

  const projectResponse = page.waitForResponse(
    (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
  );
  await page
    .getByRole("combobox", { name: "Project", exact: true })
    .selectOption({ label: "Zephyr" });
  const zephyr = await projectResponse;
  const zephyrQuery = new URL(zephyr.url()).searchParams;
  assert.equal(zephyr.status(), 200);
  assert.equal(zephyrQuery.get("profile"), "Personal");
  assert.ok(zephyrQuery.get("project"));
  assert.match(await page.getByRole("table").innerText(), /Zephyr/);
  assert.doesNotMatch(await page.getByRole("table").innerText(), /Atlas Research/);

  const filteredTabs = [
    ["Models", "Models", /gpt-5[\s\S]*1/],
    ["Activity", "Activity", /Observed Session/],
    ["Tokens", "Token observations", /84/],
    ["Projects", "Projects", /Zephyr[\s\S]*1/],
  ];
  for (const [tabName, heading, expected] of filteredTabs) {
    const filteredResponse = page.waitForResponse(
      (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
    );
    await page.getByRole("button", { name: tabName, exact: true }).click();
    const filtered = await filteredResponse;
    const query = new URL(filtered.url()).searchParams;
    assert.equal(query.get("profile"), "Personal");
    assert.equal(query.get("project"), zephyrQuery.get("project"));
    await page.getByRole("heading", { name: heading, exact: true }).first().waitFor();
    assert.match(await page.getByRole("table").innerText(), expected);
  }

  const atlasResponse = page.waitForResponse(
    (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
  );
  await page
    .getByRole("combobox", { name: "Project", exact: true })
    .selectOption({ label: "Atlas Research" });
  assert.equal((await atlasResponse).status(), 200);
  const unsupportedTokenResponse = page.waitForResponse(
    (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
  );
  await page.getByRole("button", { name: "Tokens", exact: true }).click();
  assert.equal((await unsupportedTokenResponse).status(), 200);
  await page
    .getByText("Token metrics are unsupported for the records matching these filters.", {
      exact: true,
    })
    .waitFor();
  const unsupportedModelResponse = page.waitForResponse(
    (item) => item.url().includes("/api/v1/activity") && item.request().method() === "GET",
  );
  await page.getByRole("button", { name: "Models", exact: true }).click();
  assert.equal((await unsupportedModelResponse).status(), 200);
  await page
    .getByText("Model metadata is unsupported for the records matching these filters.", {
      exact: true,
    })
    .waitFor();
  check(
    "all analytics tabs share profile, history, and Project Alias filters, send supported service filters, preserve exact zeroes, and state absent dimensions explicitly",
  );
  check(
    "Project Alias editing uses the authenticated generated client and never projects canonical paths",
  );
  check(
    "390px Projects reflow and Chromium 200% page scale keep keyboard disclosures and alias editing operable without essential horizontal overflow",
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

function requestProjectId(payload) {
  assert.equal(payload.projects.length, 2);
  return payload.projects.find((project) => project.alias === "Atlas Research").project_id;
}
