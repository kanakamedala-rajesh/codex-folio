/* global document, fetch */
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
  assert.ok(
    records.some((record) => record.record_type === "observed_session" && !record.profile_id),
  );
  const combinedResponse = await page.request.get(
    new URL("/api/v1/analytics?scope=combined_identity", link).href,
  );
  assert.equal(combinedResponse.status(), 200);
  const combinedActivity = (await combinedResponse.json()).activity;
  assert.ok(combinedActivity.length > 0);
  assert.ok(
    combinedActivity.every((record) => record.profile_id),
    "Combined Identity View excludes Unassigned History",
  );
  const reviewButton = page.getByRole("button", { name: "Review sources", exact: true });
  await reviewButton.focus();
  await page.keyboard.press("Enter");
  const reviewedSources = await (
    await page.request.get(new URL("/api/v1/activity/sources", link).href)
  ).json();
  assert.ok(Array.isArray(reviewedSources.sources));
  for (const source of reviewedSources.sources) {
    const card = page.getByRole("region", { name: source.label, exact: true });
    await card.waitFor();
    if (source.status === "supported") {
      assert.equal(await card.getByRole("button", { name: "Import source" }).isDisabled(), true);
    } else {
      assert.equal(await card.getByRole("button", { name: "Import source" }).count(), 0);
    }
  }
  await page
    .getByRole("region", { name: "Default Codex home", exact: true })
    .getByText(
      "This source format is unsupported. Update CodexFolio or choose a supported local source.",
    )
    .waitFor();
  await page
    .getByRole("region", { name: "Configured Codex home", exact: true })
    .getByText(
      "This source has an unsupported schema. Update CodexFolio or choose a supported local source.",
    )
    .waitFor();
  assert.deepEqual(
    (await (await page.request.get(new URL("/api/v1/activity", link).href)).json()).records,
    records,
    "review and unchecked consent leave activity unchanged",
  );
  const observed = records.filter((record) => record.record_type === "observed_session");
  assert.equal(observed.length, 4);
  const correlated = observed.find((record) => record.tokens_used === "42");
  assert.ok(correlated);
  assert.equal(correlated.correlation_state, "correlated");
  assert.ok(correlated.profile_id);
  assert.ok(observed.some((record) => !record.profile_id));
  assert.ok(observed.some((record) => !record.profile_id && !record.model));
  const zero = observed.find((record) => record.tokens_used === "0");
  assert.ok(zero, "source fixture includes recorded zero usage");
  assert.equal(
    zero.historical_metrics?.find((metric) => metric.metric_key === "codex.local.tokens_used")
      ?.value,
    "0",
  );
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
  assert.equal(
    await rows().count(),
    observed.filter((record) => record.profile_id === correlated.profile_id).length,
  );
  await select("Project", correlated.project_id);
  assert.equal(
    await rows().count(),
    observed.filter(
      (record) =>
        record.profile_id === correlated.profile_id && record.project_id === correlated.project_id,
    ).length,
  );
  await capture("sessions-filtered-wide", 1440, 1000);
  await scanAccessibility("sessions");
  await rows().first().getByRole("button", { name: "Open details", exact: true }).focus();
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.activeElement?.tagName === "H1");
  let text = await page.locator("main").innerText();
  assert.match(text, /Observed Session · Work/);
  assert.match(text, /42 tokens · available · local_metadata/);
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
  await select("Profile", "__unassigned__");
  await select("Project", "");
  assert.equal(await rows().count(), observed.filter((record) => !record.profile_id).length);
  await rows().first().getByRole("button", { name: "Open details", exact: true }).click();
  text = await page.locator("main").innerText();
  assert.match(text, /Observed Session · Unassigned History/);
  assert.match(text, /Unknown ownership/);
  assert.match(text, /Partial metadata/);
  assert.match(text, /0 tokens · available · local_metadata/);
  assert.match(text, /Uncorrelated/);
  assert.match(text, /Unavailable/);
  await capture("session-partial-wide", 1440, 1000);
  await page.getByRole("button", { name: "Back to Sessions", exact: true }).click();
  await select("Date range", "custom");
  const localDay = await page.evaluate((value) => {
    const date = new Date(value);
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
  }, correlated.started_at);
  const unassignedOnDay = await page.evaluate(
    ({ records, day }) =>
      records.filter((record) => {
        if (record.profile_id) return false;
        const date = new Date(record.started_at);
        const local = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
        return local === day;
      }).length,
    { records: observed, day: localDay },
  );
  await page.getByLabel("From date", { exact: true }).fill(localDay);
  await page.getByLabel("Through date", { exact: true }).fill(localDay);
  assert.equal(await rows().count(), unassignedOnDay);
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
  await reviewButton.focus();
  await page.keyboard.press("Enter");
  const supportedSource = reviewedSources.sources.find((source) => source.status === "supported");
  if (supportedSource) {
    const card = page.getByRole("region", { name: supportedSource.label, exact: true });
    const consent = card.getByRole("checkbox", {
      name: "I choose to import this source's supported session metadata.",
      exact: true,
    });
    await consent.focus();
    await page.keyboard.press("Space");
    await card.locator("button:enabled").filter({ hasText: "Import source" }).waitFor();
  }
  await scanAccessibility("sessions-source-review-narrow");
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
  const workSource = reviewedSources.sources.find(
    (source) => source.label === "Work" && source.status === "supported",
  );
  const personalSource = reviewedSources.sources.find(
    (source) => source.label === "Personal" && source.status === "supported",
  );
  assert.ok(workSource && personalSource, "fixture exposes both supported homes");
  await reviewButton.click();
  const importOnce = async (source) => {
    const card = page.getByRole("region", { name: source.label, exact: true });
    await card.waitFor();
    await card.getByRole("checkbox", { name: /I choose to import this source/ }).check();
    const imported = page.waitForResponse(
      (item) =>
        new URL(item.url()).pathname === "/api/v1/activity/sources" &&
        item.request().method() === "POST",
    );
    await card.getByRole("button", { name: "Import source", exact: true }).click();
    const response = await imported;
    assert.equal(response.status(), 200, await response.text());
    const result = await response.json();
    await page.getByRole("status").filter({ hasText: "Import complete." }).waitFor();
    return result;
  };
  const workImport = await importOnce(workSource);
  assert.equal(workImport.imported_count, 2, "Work imports a shared and a distinct new session");
  const personalImport = await importOnce(personalSource);
  assert.equal(personalImport.imported_count, 1, "Personal imports only its distinct new session");
  assert.ok(personalImport.already_present_count > 0, "shared session is already present");
  const afterMixedImport = (
    await (await page.request.get(new URL("/api/v1/activity", link).href)).json()
  ).records;
  assert.equal(afterMixedImport.length, records.length + 3);
  for (const id of [
    "018f4f70-6f77-7c3f-9b77-93aa087dfc51",
    "018f4f70-6f77-7c3f-9b77-93aa087dfc52",
    "018f4f70-6f77-7c3f-9b77-93aa087dfc53",
  ]) {
    const matching = afterMixedImport.filter((record) => record.source_session_id === id);
    assert.equal(matching.length, 1, `${id} occurs once across source homes`);
    assert.equal(matching[0].profile_id, "", "import does not infer ownership");
    assert.deepEqual(
      matching[0].historical_metrics?.map((metric) => ({
        metric_key: metric.metric_key,
        value: metric.value,
        unit: metric.unit,
        availability: metric.availability,
        source: metric.source,
        freshness: metric.freshness,
      })),
      [
        {
          metric_key: "codex.local.tokens_used",
          value: "7",
          unit: "tokens",
          availability: "available",
          source: "local_metadata",
          freshness: "historical",
        },
      ],
    );
  }
  const overallResponse = await page.request.get(
    new URL("/api/v1/analytics?scope=overall_history", link).href,
  );
  assert.equal(overallResponse.status(), 200);
  const overall = await overallResponse.json();
  assert.equal(overall.scope, "overall_history");
  assert.ok(
    overall.historical_metrics.some(
      (metric) =>
        metric.metric_key === "codex.local.tokens_used" &&
        metric.unit === "tokens" &&
        metric.unassigned_session_count >= 3 &&
        BigInt(metric.unassigned_value) >= 21n &&
        metric.measured_session_count >= 3,
    ),
    `overall history includes supported imported Unassigned values and coverage: ${JSON.stringify(overall.historical_metrics)}`,
  );
  assert.ok(
    !overall.historical_metrics.some((metric) => /credit|quota|percent/.test(metric.metric_key)),
  );
  await nav("Analytics").click();
  await page.getByRole("button", { name: "Tokens", exact: true }).click();
  await page.getByRole("combobox", { name: "Analytics scope" }).selectOption("");
  await page.getByRole("region", { name: "Historical token coverage" }).waitFor();
  const coverage = await page
    .getByRole("region", { name: "Historical token coverage" })
    .innerText();
  assert.match(coverage, /Unassigned History contribution/);
  assert.match(coverage, /observed sessions have a supported token value/);
  await scanAccessibility("analytics-overall-history");
  await page.setViewportSize({ width: 390, height: 844 });
  await scanAccessibility("analytics-overall-history-narrow");
  await page.setViewportSize({ width: 1440, height: 1000 });
  await nav("Sessions").click();
  await page.getByRole("status").filter({ hasText: "matching records" }).waitFor();
  await reviewButton.click();
  for (const source of [workSource, personalSource]) {
    const repeated = await importOnce(source);
    assert.equal(repeated.imported_count, 0, "repeat import adds no source sessions");
    assert.ok(repeated.already_present_count > 0, "repeat import reports existing sessions");
  }
  await page.getByRole("status").filter({ hasText: "0 new sessions were added" }).waitFor();
  assert.deepEqual(
    (await (await page.request.get(new URL("/api/v1/activity", link).href)).json()).records,
    afterMixedImport,
    "repeat import leaves normalized activity unchanged",
  );
  const profileResponse = await page.request.get(new URL("/api/v1/profiles", link).href);
  assert.equal(profileResponse.status(), 200);
  const readyProfiles = (await profileResponse.json()).profiles.filter(
    (profile) => profile.status === "ready",
  );
  const work = readyProfiles.find((profile) => profile.alias === "Work");
  const personal = readyProfiles.find((profile) => profile.alias === "Personal");
  assert.ok(work && personal);
  await select("Profile", "__unassigned__");
  const choices = page
    .getByRole("table", { name: "Metadata timeline" })
    .getByRole("checkbox", { name: "Select for assignment" });
  await choices.nth(0).check();
  await choices.nth(1).check();
  const assignment = page.getByRole("region", { name: "Assign selected sessions" });
  await assignment.getByRole("combobox", { name: "Assign to" }).selectOption(work.profile_id);
  const authorizedAssignment = page.waitForRequest(
    (request) =>
      new URL(request.url()).pathname === "/api/v1/activity/assignments" &&
      request.method() === "POST",
  );
  await assignment.getByRole("button", { name: "Save 2 selected assignments" }).click();
  const csrf = (await authorizedAssignment).headers()["x-codexfolio-csrf"];
  assert.ok(csrf, "UI assignment sends a CSRF token");
  await assignment
    .getByRole("status")
    .filter({ hasText: "Ownership updated for 2 sessions" })
    .waitFor();
  const assigned = (await (await page.request.get(new URL("/api/v1/activity", link).href)).json())
    .records;
  const newlyAssigned = assigned.filter(
    (record) =>
      record.attribution_provenance === "user_assigned" && record.profile_id === work.profile_id,
  );
  assert.equal(newlyAssigned.length, 2);
  const workAnalytics = await page.request.get(
    new URL(`/api/v1/activity?profile=${work.alias}`, link).href,
  );
  assert.equal(workAnalytics.status(), 200);
  assert.equal(
    (await workAnalytics.json()).records.filter((record) =>
      newlyAssigned.some((item) => item.id === record.id),
    ).length,
    2,
  );
  await select("Profile", "");
  const assignedRow = page
    .getByRole("table", { name: "Metadata timeline" })
    .getByRole("row")
    .filter({ hasText: "Observed Session" })
    .first();
  await assignedRow.getByRole("button", { name: "Open details" }).click();
  const detail = page.getByRole("region", { name: "Correct ownership" });
  await detail.getByRole("combobox", { name: "Assign to" }).selectOption(personal.profile_id);
  await detail.getByRole("button", { name: "Save assignment" }).click();
  await detail.getByRole("status").filter({ hasText: "Ownership updated" }).waitFor();
  await page.getByRole("button", { name: "Back to Sessions" }).click();
  const corrected = (await (await page.request.get(new URL("/api/v1/activity", link).href)).json())
    .records;
  assert.ok(
    corrected.some(
      (record) =>
        record.attribution_provenance === "user_assigned" &&
        record.profile_id === personal.profile_id,
    ),
  );
  for (const source of [workSource, personalSource]) await importOnce(source);
  const stable = (await (await page.request.get(new URL("/api/v1/activity", link).href)).json())
    .records;
  assert.deepEqual(stable, corrected, "reimport preserves user corrections");
  const assignmentURL = new URL("/api/v1/activity/assignments", link).href;
  const combinedURL = new URL("/api/v1/analytics?scope=combined_identity", link).href;
  const historyURL = new URL("/api/v1/activity", link).href;
  const profileHistory = async (alias) => {
    const url = new URL(historyURL);
    url.searchParams.set("profile", alias);
    const response = await page.request.get(url.href);
    assert.equal(response.status(), 200);
    return (await response.json()).records;
  };
  const combinedHistory = async () => {
    const response = await page.request.get(combinedURL);
    assert.equal(response.status(), 200);
    return (await response.json()).activity;
  };
  const beforeReturnWork = await profileHistory(work.alias);
  const beforeReturnPersonal = await profileHistory(personal.alias);
  const beforeReturnCombined = await combinedHistory();
  const workSession = newlyAssigned.find((record) =>
    stable.some((item) => item.id === record.id && item.profile_id === work.profile_id),
  );
  assert.ok(workSession, "one bulk-assigned session remains in Work");
  await select("Profile", work.profile_id);
  await page
    .getByRole("table", { name: "Metadata timeline" })
    .locator(`button[data-session-key="observed_session:${workSession.id}"]`)
    .click();
  await detail.getByRole("combobox", { name: "Assign to" }).selectOption("");
  await detail.getByRole("button", { name: "Save assignment" }).click();
  await detail.getByRole("status").filter({ hasText: "Ownership updated" }).waitFor();
  await page.getByRole("button", { name: "Back to Sessions" }).click();
  const returned = (await (await page.request.get(historyURL)).json()).records;
  const returnedSession = returned.find((record) => record.id === workSession.id);
  assert.equal(returnedSession.profile_id, "");
  assert.equal(returnedSession.attribution_provenance, "user_assigned");
  const afterReturnWork = await profileHistory(work.alias);
  const afterReturnPersonal = await profileHistory(personal.alias);
  assert.equal(afterReturnWork.length, beforeReturnWork.length - 1);
  assert.ok(!afterReturnWork.some((record) => record.id === workSession.id));
  assert.deepEqual(afterReturnPersonal, beforeReturnPersonal);
  const afterReturnCombined = await combinedHistory();
  assert.equal(afterReturnCombined.length, beforeReturnCombined.length - 1);
  assert.ok(!afterReturnCombined.some((record) => record.id === workSession.id));
  await select("Profile", "__unassigned__");
  assert.equal(
    await rows().locator(`button[data-session-key="observed_session:${workSession.id}"]`).count(),
    1,
  );
  const invalidAssignment = await page.request.post(assignmentURL, {
    data: {
      session_ids: [workSession.id, "missing-observed-session"],
      profile_id: personal.profile_id,
    },
    headers: { Origin: new URL(link).origin, "X-CodexFolio-CSRF": csrf },
  });
  assert.equal(invalidAssignment.status(), 409);
  assert.deepEqual((await (await page.request.get(historyURL)).json()).records, returned);
  assert.deepEqual(await profileHistory(work.alias), afterReturnWork);
  assert.deepEqual(await profileHistory(personal.alias), afterReturnPersonal);
  assert.deepEqual(await combinedHistory(), afterReturnCombined);
  const unauthenticatedStatus = await page.evaluate(
    async ({ url, id, profileId }) =>
      (
        await fetch(url, {
          method: "POST",
          credentials: "omit",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ session_ids: [id], profile_id: profileId }),
        })
      ).status,
    { url: assignmentURL, id: workSession.id, profileId: personal.profile_id },
  );
  assert.equal(unauthenticatedStatus, 401);
  const csrfLessAssignment = await page.request.post(assignmentURL, {
    data: { session_ids: [workSession.id], profile_id: personal.profile_id },
    headers: { Origin: new URL(link).origin },
  });
  assert.equal(csrfLessAssignment.status(), 403);
  assert.deepEqual((await (await page.request.get(historyURL)).json()).records, returned);
  assert.deepEqual(await profileHistory(work.alias), afterReturnWork);
  assert.deepEqual(await profileHistory(personal.alias), afterReturnPersonal);
  assert.deepEqual(await combinedHistory(), afterReturnCombined);
  await nav("Overview").click();
  check(
    "Sessions source review, consent, mixed-home and repeat import with shared-session dedup; individual and bulk ownership correction, return to Unassigned, atomic validation, unauthorized and CSRF rejection, reimport stability, profile and combined totals, filters, metadata, keyboard return and responsive reflow",
  );
}
