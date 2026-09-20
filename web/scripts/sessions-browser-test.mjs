/* global document */
import { URL } from "node:url";
import assert from "node:assert/strict";
import { readFileSync, writeFileSync } from "node:fs";
import { setTimeout } from "node:timers/promises";

export async function testSessions({
  page,
  context,
  link,
  control,
  capture,
  scanAccessibility,
  check,
}) {
  const nav = (name) =>
    page
      .getByRole("navigation", { name: "Primary", exact: true })
      .getByRole("button", { name, exact: true });
  await nav("Sessions").focus();
  await page.keyboard.press("Enter");
  await page.getByRole("combobox", { name: "Record type", exact: true }).waitFor();
  await page.getByRole("button", { name: "Reload timeline", exact: true }).isEnabled();
  await page.getByRole("status").filter({ hasText: "matching records" }).waitFor();
  const response = await page.request.get(new URL("/api/v1/activity", link).href);
  assert.equal(response.status(), 200);
  const raw = await response.text();
  for (const excluded of [
    "canonical_path",
    "identity_home",
    "private title",
    "private preview",
    "private prompt",
    "credential sentinel",
    "command sentinel",
    "first_user_message",
    "future_unknown",
  ])
    assert.ok(!raw.includes(excluded), excluded);
  const records = JSON.parse(raw).records;
  const observed = records.filter((record) => record.record_type === "observed_session");
  assert.equal(observed.length, 4);
  const correlated = observed.find((record) => record.correlation_state === "correlated");
  const contradictory = observed.find((record) => record.correlation_state === "contradictory");
  assert.ok(correlated && contradictory);
  const related = records.find((record) => record.id === correlated.correlation_managed_launch_id);
  assert.equal(related.record_type, "managed_launch");
  assert.equal(related.tokens_used, "");
  assert.equal(correlated.tokens_used, "42");
  assert.notEqual(correlated.started_at, related.started_at);
  const selectionBefore = await (
    await page.request.get(new URL("/api/v1/selection", link).href)
  ).json();
  const select = (name, value) =>
    page.getByRole("combobox", { name, exact: true }).selectOption(value);
  const rows = () =>
    page.getByRole("table", { name: "Metadata timeline", exact: true }).locator("tbody tr");
  await select("Record type", "observed_session");
  assert.equal(await rows().count(), 4);
  await select("Profile", correlated.profile_id);
  assert.equal(await rows().count(), 3);
  await select("Project", correlated.project_id);
  assert.equal(await rows().count(), 2);
  await capture("sessions-filtered-wide", 1440, 1000);
  await scanAccessibility("sessions");
  await rows().nth(1).getByRole("button", { name: "Open details", exact: true }).focus();
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.activeElement?.tagName === "H1");
  let text = await page.locator("main").innerText();
  assert.match(text, /Observed Session · Work/);
  assert.match(text, /42 · Locally derived/);
  assert.match(text, /Explicit source session ID/);
  assert.match(text, /High/);
  assert.match(text, /Launch Profile \(immutable\)/);
  assert.match(
    text,
    /Token and model observations are unavailable from this Managed Launch record/,
  );
  assert.match(text, /Correlation is not causation/);
  await capture("session-detail-wide", 1440, 1000);
  await scanAccessibility("session-detail");
  await page.getByRole("button", { name: "Back to Sessions", exact: true }).click();
  await page.waitForFunction(() => document.activeElement?.textContent === "Open details");
  assert.equal(await rows().count(), 2);
  await rows().first().getByRole("button", { name: "Open details", exact: true }).click();
  text = await page.locator("main").innerText();
  assert.match(text, /Contradictory/);
  assert.match(text, /No supported relationship to another record is established/);
  assert.ok(!text.includes("Launch Profile (immutable)"));
  await page.getByRole("button", { name: "Back to Sessions", exact: true }).click();
  await select("Project", "");
  await rows().first().getByRole("button", { name: "Open details", exact: true }).click();
  text = await page.locator("main").innerText();
  assert.match(text, /Partial metadata/);
  assert.match(text, /0 · Locally derived/);
  assert.match(text, /Uncorrelated/);
  assert.match(text, /Unavailable/);
  await capture("session-partial-wide", 1440, 1000);
  await page.getByRole("button", { name: "Back to Sessions", exact: true }).click();
  await select("Date range", "custom");
  const localDay = await page.evaluate((value) => {
    const date = new Date(value);
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
  }, correlated.started_at);
  await page.getByLabel("From date", { exact: true }).fill(localDay);
  await page.getByLabel("Through date", { exact: true }).fill(localDay);
  assert.equal(await rows().count(), 3);
  await page.getByLabel("From date", { exact: true }).fill("2099-01-01");
  await page.getByRole("alert").filter({ hasText: "Choose a through date" }).waitFor();
  assert.equal(await rows().count(), 0);
  await page.getByLabel("Through date", { exact: true }).fill("2099-01-01");
  await page
    .getByText("No records match these filters. Missing history does not mean zero activity.", {
      exact: true,
    })
    .waitFor();
  await select("Date range", "all");
  await select("Profile", "");
  await select("Record type", "");
  const selectionAfter = await (
    await page.request.get(new URL("/api/v1/selection", link).href)
  ).json();
  assert.equal(selectionAfter.profile_id, selectionBefore.profile_id);
  await capture("sessions-wide", 1440, 1000);
  await capture("sessions-medium", 900, 1000);
  await capture("sessions-narrow", 390, 844);
  const disclosure = page.locator("main details").first();
  await disclosure.locator("summary").focus();
  await page.keyboard.press("Enter");
  await disclosure.getByRole("button", { name: "Open details", exact: true }).focus();
  await page.keyboard.press("Enter");
  await capture("session-detail-narrow", 390, 844);
  await scanAccessibility("session-detail-narrow");
  await page.getByRole("button", { name: "Back to Sessions", exact: true }).click();
  await page.waitForFunction(() => document.activeElement?.textContent === "Open details");
  await scanAccessibility("sessions-narrow");
  await page.emulateMedia({ colorScheme: "light" });
  await capture("sessions-light", 1440, 1000);
  await scanAccessibility("sessions-light");
  await page.emulateMedia({ forcedColors: "active", contrast: "more", reducedMotion: "reduce" });
  await capture("sessions-forced", 390, 844);
  await disclosure.getByRole("button", { name: "Open details", exact: true }).click();
  await capture("session-detail-forced", 390, 844);
  await page.getByRole("button", { name: "Back to Sessions", exact: true }).click();
  await page.emulateMedia({
    colorScheme: "dark",
    forcedColors: "none",
    contrast: "no-preference",
    reducedMotion: "no-preference",
  });
  await capture("sessions-zoom-reflow", 720, 500);
  await page.setViewportSize({ width: 1440, height: 1000 });
  await context.setOffline(true);
  await page.getByRole("button", { name: "Reload timeline", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "Reload failed" }).waitFor();
  assert.equal(await rows().count(), records.length);
  await context.setOffline(false);
  await page.getByRole("button", { name: "Reload timeline", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "matching records" }).waitFor();
  const forbiddenQuery = await page.request.get(
    new URL("/api/v1/activity?include_paths=true", link).href,
  );
  assert.equal(forbiddenQuery.status(), 400);
  const forbiddenWrite = await page.request.post(new URL("/api/v1/command/activity", link).href, {
    data: { action: "refresh", alias: "Work" },
    headers: { Origin: new URL(link).origin },
  });
  assert.equal(forbiddenWrite.status(), 401);
  await select("Record type", "observed_session");
  await nav("Overview").click();
  await nav("Sessions").click();
  await page.getByRole("status").filter({ hasText: "matching records" }).waitFor();
  assert.equal(
    await page.getByRole("combobox", { name: "Record type", exact: true }).inputValue(),
    "observed_session",
  );
  const paginationControl = async (action) => {
    writeFileSync(control, action);
    for (let attempt = 0; attempt < 3000; attempt++) {
      const state = readFileSync(control, "utf8");
      assert.notEqual(state, `${action}-failed`);
      if (state === `${action}-done`) return;
      await setTimeout(10);
    }
    assert.fail(`pagination fixture did not complete ${action}`);
  };
  await paginationControl("sessions-page-seed");
  const seededTimeline = page.waitForResponse(
    (item) =>
      new URL(item.url()).pathname === "/api/v1/activity" && item.request().method() === "GET",
  );
  await page.getByRole("button", { name: "Reload timeline", exact: true }).click();
  const seededResponse = await seededTimeline;
  assert.equal(seededResponse.status(), 200);
  const seededRecords = (await seededResponse.json()).records;
  assert.equal(seededRecords.length, records.length + 26);
  const paginationRecord = seededRecords.find(
    (record) => record.source_version === "pagination-fixture-v1",
  );
  assert.ok(paginationRecord);
  const paginationDay = await page.evaluate((value) => {
    const date = new Date(value);
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
  }, paginationRecord.started_at);
  await select("Date range", "custom");
  await page.getByLabel("From date", { exact: true }).fill(paginationDay);
  await page.getByLabel("Through date", { exact: true }).fill(paginationDay);
  await page.getByRole("status").filter({ hasText: "26 matching records" }).waitFor();
  assert.equal(await rows().count(), 25);
  const pageKeys = () =>
    rows()
      .locator("button[data-session-key]")
      .evaluateAll((buttons) => buttons.map((button) => button.getAttribute("data-session-key")));
  const firstPageKeys = await pageKeys();
  const previous = page.getByRole("button", { name: "Previous page", exact: true });
  const next = page.getByRole("button", { name: "Next page", exact: true });
  assert.equal(await previous.isDisabled(), true);
  await next.focus();
  await page.keyboard.press("Enter");
  assert.equal(await rows().count(), 1);
  const secondPageKeys = await pageKeys();
  assert.equal(new Set([...firstPageKeys, ...secondPageKeys]).size, 26);
  assert.equal(await next.isDisabled(), true);
  await previous.click();
  assert.deepEqual(await pageKeys(), firstPageKeys);
  await next.click();
  await select("Date range", "all");
  assert.equal(await previous.isDisabled(), true);
  assert.equal(await rows().count(), 25);
  await paginationControl("sessions-page-clean");
  const cleanedTimeline = page.waitForResponse(
    (item) =>
      new URL(item.url()).pathname === "/api/v1/activity" && item.request().method() === "GET",
  );
  await page.getByRole("button", { name: "Reload timeline", exact: true }).click();
  const cleanedResponse = await cleanedTimeline;
  assert.equal(cleanedResponse.status(), 200);
  assert.deepEqual((await cleanedResponse.json()).records, records);
  await page.getByRole("status").filter({ hasText: "4 matching records" }).waitFor();
  assert.equal(await next.count(), 0);
  check(
    "real-service Sessions pagination covers 26 independent records, keyboard Next, Previous, boundary disabling, filter reset and exact fixture cleanup",
  );
  await nav("Overview").click();
  check(
    "Sessions filters, independent correlated/contradictory/partial facts, safe metadata, keyboard return, retained filters, themes, responsive reflow and failed-read recovery",
  );
}
