/* global fetch */
import assert from "node:assert/strict";

export async function testPartialStartup({ page, link, capture, check, scanAccessibility }) {
  let releaseAnalytics;
  let releaseAlerts;
  const analyticsPending = new Promise((resolve) => {
    releaseAnalytics = resolve;
  });
  const alertsPending = new Promise((resolve) => {
    releaseAlerts = resolve;
  });
  const analyticsRoute = async (route) => {
    if (route.request().headers()["x-fixture-measurement"]) return route.continue();
    await analyticsPending;
    await route.abort("failed");
  };
  const alertsRoute = async (route) => {
    if (route.request().headers()["x-fixture-measurement"]) return route.continue();
    await alertsPending;
    await route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({
        code: "CF_HTTPAPI_SERVICE_UNAVAILABLE",
        message: "Fixture alert failure",
      }),
    });
  };
  await page.route("**/api/v1/analytics?*", analyticsRoute);
  await page.route("**/api/v1/alerts", alertsRoute);
  await page.goto(link);
  const navigate = async (name) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page
      .getByRole("navigation", { name: "Primary", exact: true })
      .getByRole("button", { name, exact: true })
      .click();
  };
  const noRelaunch = async () => {
    assert.equal(
      await page.getByRole("heading", { name: "Relaunch CodexFolio", exact: true }).count(),
      0,
    );
  };
  await page.getByRole("status").filter({ hasText: "Loading analytics." }).waitFor();
  await page.getByRole("status").filter({ hasText: "Loading alerts." }).waitFor();
  // Separate fetches pass through interception to the actual HTTPS service.
  const reads = await page.evaluate(async () =>
    Promise.all(
      ["/api/v1/analytics?scope=combined_identity", "/api/v1/alerts"].map(async (path) => {
        const started = Date.now();
        const response = await fetch(path, { headers: { "X-Fixture-Measurement": "1" } });
        const body = await response.json();
        return { path, status: response.status, elapsed: Date.now() - started, body };
      }),
    ),
  );
  for (const read of reads) assert.equal(read.status, 200, `retained-history ${read.path}`);
  assert.ok(reads[0].body.activity.length >= 806, "representative retained sessions missing");
  assert.ok(reads[0].body.recent.length >= 24, "retained usage captures missing");
  for (const read of reads) {
    assert.ok(read.elapsed < 10_000, "retained-history route exceeded request budget");
    check(`concurrent HTTPS ${read.path}: ${read.elapsed} ms with 806 retained sessions`);
  }
  await navigate("Profiles");
  await page.getByRole("button", { name: "Personal", exact: true }).waitFor();
  assert.equal(
    await page
      .getByRole("combobox", { name: "Dashboard Scope", exact: true })
      .filter({ visible: true })
      .inputValue(),
    "Work",
    "pending analytics must preserve the visible selected scope",
  );
  await capture("startup-pending-profiles-wide", 1440, 1000);
  await capture("startup-pending-profiles-narrow", 390, 844);
  await noRelaunch();
  await navigate("Settings");
  await page
    .getByText("Unlocked for this service session · Database ready", { exact: true })
    .waitFor();
  await capture("startup-pending-settings-narrow", 390, 844);
  check("pending analytics and alerts preserve authenticated profiles and service settings");

  releaseAnalytics();
  await page.getByRole("button", { name: "Retry analytics", exact: true }).waitFor();
  // Analytics failure must settle independently while alerts remain pending.
  await page.getByRole("status").filter({ hasText: "Loading alerts." }).waitFor();
  releaseAlerts();
  await page.getByRole("button", { name: "Retry alerts", exact: true }).waitFor();
  await noRelaunch();
  await scanAccessibility("startup-optional-failure");
  await capture("startup-failed-settings-wide", 1440, 1000);
  await capture("startup-failed-settings-narrow", 390, 844);
  await navigate("Profiles");
  await page.getByRole("button", { name: "Personal", exact: true }).waitFor();
  check(
    "network analytics failure and HTTP alert failure retain core content and accessible retries",
  );

  await page.unroute("**/api/v1/analytics?*", analyticsRoute);
  await page.getByRole("button", { name: "Retry analytics", exact: true }).click();
  await page
    .getByRole("button", { name: "Retry analytics", exact: true })
    .waitFor({ state: "hidden" });
  await page
    .getByRole("status")
    .filter({ hasText: "Loading analytics." })
    .waitFor({ state: "hidden" });
  await navigate("Overview");
  await page.getByRole("heading", { name: "Current capacity", exact: true }).waitFor();
  await page.getByRole("button", { name: "Retry alerts", exact: true }).waitFor();
  await page.unroute("**/api/v1/alerts", alertsRoute);
  await page.getByRole("button", { name: "Retry alerts", exact: true }).click();
  await page
    .getByRole("status")
    .filter({ hasText: "Loading alerts." })
    .waitFor({ state: "hidden" });
  await navigate("Alerts");
  await page.getByRole("heading", { name: "Operational alerts", exact: true }).waitFor();
  await capture("startup-recovered-alerts-narrow", 390, 844);
  check("optional panels recover independently through retry without restarting service");

  await page.route("**/api/v1/analytics?*", (route) =>
    route.fulfill({
      status: 401,
      contentType: "application/json",
      body: JSON.stringify({
        code: "CF_HTTPAPI_SESSION_EXPIRED",
        message: "Fixture expired session",
      }),
    }),
  );
  await navigate("Overview");
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await page.getByRole("heading", { name: "Relaunch CodexFolio", exact: true }).waitFor();
  check("genuine authorization expiry still requires a fresh terminal link");
}
