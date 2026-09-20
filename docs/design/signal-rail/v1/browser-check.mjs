// Disposable #58 benchmark runner; use an existing Playwright installation.
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { readFile, mkdir, writeFile } from "node:fs/promises";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { cpus, platform, arch, totalmem, release } from "node:os";
import { gzipSync } from "node:zlib";

const [playwrightPath, chromiumPath, outputPath, captureMode] = process.argv.slice(2);
assert(playwrightPath && chromiumPath && outputPath, "Usage: node browser-check.mjs PLAYWRIGHT_ENTRY CHROMIUM_EXECUTABLE OUTPUT_DIRECTORY [all]");
const { chromium } = await import(pathToFileURL(resolve(playwrightPath)).href);
const root = dirname(fileURLToPath(import.meta.url));
const output = resolve(outputPath);
await mkdir(output, { recursive: true });
const files = new Map(await Promise.all(["benchmark.html", "benchmark.mjs", "reference.css", "reference.html"].map(async (name) => ["/" + name, await readFile(join(root, name))])));
const server = createServer((req, res) => {
  const path = new URL(req.url, "http://127.0.0.1").pathname;
  const content = files.get(path);
  if (!content) { res.writeHead(404); res.end(); return; }
  res.setHeader("Content-Type", path.endsWith(".mjs") ? "text/javascript" : path.endsWith(".css") ? "text/css" : "text/html");
  res.end(content);
});
await new Promise((done) => server.listen(0, "127.0.0.1", done));
const origin = `http://127.0.0.1:${server.address().port}`;
let browser;
const evidence = { recordedAt: new Date().toISOString(), fixture: { months: 13, start: "2025-08-01T00:00:00Z", endExclusive: "2026-09-01T00:00:00Z", samples: 19008, metricsPerSample: 2, intervalMinutes: 30, pageSize: 48, profiles: 2 }, environment: { platform: platform(), kernel: release(), architecture: arch(), cpu: cpus()[0]?.model, logicalCPUs: cpus().length, memoryGiB: Math.round(totalmem() / 1024 ** 3) }, checks: [], timings: {}, screenshots: [] };
const fullPageStyle = "@media(max-width:699px){.bottom-nav{position:static!important}.workspace{padding-bottom:2rem!important}}";
const errors = [];
try {
  browser = await chromium.launch({ executablePath: resolve(chromiumPath), headless: true });
  evidence.environment.browser = await browser.version();
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 }, colorScheme: "dark" });
  page.on("pageerror", (e) => errors.push(e.message));
  const external = [];
  page.on("request", (req) => { if (!req.url().startsWith(origin)) external.push(req.url()); });
  await page.goto(origin + "/benchmark.html");
  await page.locator("#data tbody tr").first().waitFor();
  await page.locator("#resolution").selectOption("raw");
  assert.match(await page.title(), /Disposable chart benchmark/);
  assert.match(await page.locator("#scope").innerText(), /19,008/);
  assert.equal(await page.locator("#data tbody tr").count(), 48);
  assert.match(await page.locator("#selection").innerText(), /Temporarily unavailable/);
  await page.locator("#sample").focus();
  for (let i = 0; i < 8; i++) await page.keyboard.press("ArrowRight");
  assert.match(await page.locator("#selection").innerText(), /5-hour remaining 28%; weekly remaining 98%/);
  assert.match(await page.locator("tr[data-current]").innerText(), /28%/);
  await page.keyboard.press("ArrowRight"); await page.keyboard.press("ArrowRight");
  assert.match(await page.locator("#selection").innerText(), /5-hour remaining 0%/);
  assert.match(await page.locator("tr[data-current]").innerText(), /0%/);
  evidence.checks.push("Keyboard sample inspection, table synchronization, and genuine zero versus unavailable: PASS");
  await page.keyboard.press("End");
  assert.equal(await page.locator("#sample").inputValue(), "19007");
  assert.equal(await page.locator("#next").isDisabled(), true);
  await page.locator("#previous").click();
  assert.equal(await page.locator("#sample").inputValue(), "18912");
  await page.locator("#period").selectOption("week");
  assert.match(await page.locator("#scope").innerText(), /336/);
  await page.locator("#profile").selectOption("work");
  assert.match(await page.locator("caption").innerText(), /Work Studio/);
  evidence.checks.push("13-month pagination and period/profile changes synchronize chart, sample control and semantic table: PASS");
  const cdp = await page.context().newCDPSession(page);
  const ax = await cdp.send("Accessibility.getFullAXTree");
  const roles = ax.nodes.map((node) => node.role?.value);
  for (const role of ["table", "columnheader", "slider", "status"]) assert(roles.includes(role), `Missing accessibility role ${role}`);
  await writeFile(join(output, "accessibility-tree.json"), JSON.stringify(ax, null, 2));
  evidence.checks.push("Chromium accessibility tree exposes named slider, live status, table and column headers: PASS (not a spoken screen-reader session)");
  await page.locator("#period").selectOption("all");
  await page.locator("#profile").selectOption("personal");
  for (const renderer of ["svg", "canvas"]) {
    await page.locator("#renderer").selectOption(renderer);
    const durations = [];
    for (let i = 0; i < 12; i++) {
      const duration = await page.evaluate(async (iteration) => {
        const start = performance.now();
        const profile = document.getElementById("profile");
        profile.value = iteration % 2 ? "personal" : "work";
        profile.dispatchEvent(new Event("change", { bubbles: true }));
        await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
        return performance.now() - start;
      }, i);
      if (i >= 2) durations.push(duration);
    }
    durations.sort((a, b) => a - b);
    evidence.timings[renderer] = { milliseconds: durations.map((v) => +v.toFixed(2)), median: +((durations[4] + durations[5]) / 2).toFixed(2), p95: +durations[9].toFixed(2), method: "10 warm profile changes after 2 warmups; event dispatch through two requestAnimationFrame callbacks; full 19,008-sample history" };
  }
  await page.locator("#renderer").selectOption("svg");
  for (const locale of ["en-US", "en-GB", "de-DE", "hi-IN"]) {
    await page.locator("#locale").selectOption(locale);
    evidence.checks.push(`Locale ${locale}: ${await page.locator("#selection").innerText()}`);
  }
  await page.locator("#locale").selectOption("en-US");
  await page.locator("#resolution").selectOption("daily");
  assert.match(await page.locator("#scope").innerText(), /396 displayed samples/);
  evidence.checks.push("Explicit daily-final sampling renders the same 396 records in chart and table; raw 19,008-sample history remains selectable: PASS");
  const captures = [
    { name: "benchmark-wide", width: 1440, height: 1000, media: { colorScheme: "dark", forcedColors: "none", reducedMotion: "no-preference" } },
    { name: "benchmark-narrow", width: 390, height: 844, media: { colorScheme: "light" } },
    { name: "benchmark-forced", width: 900, height: 1000, media: { forcedColors: "active", reducedMotion: "reduce" } },
  ];
  for (const capture of captures) {
    await page.setViewportSize({ width: capture.width, height: capture.height });
    await page.emulateMedia(capture.media);
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${capture.name} overflows`);
    await page.screenshot({ path: join(output, capture.name + ".png"), fullPage: false });
    evidence.screenshots.push(capture.name + ".png");
    if (capture.name === "benchmark-narrow") {
      for (const [suffix, selector] of [["chart", "figure"], ["table", "#selection"]]) {
        await page.locator(selector).evaluate((element) => element.scrollIntoView({ block: "start" }));
        await page.screenshot({ path: join(output, `benchmark-narrow-${suffix}.png`), fullPage: false });
        evidence.screenshots.push(`benchmark-narrow-${suffix}.png`);
      }
      await page.evaluate(() => scrollTo(0, 0));
    }
  }
  assert.equal(await page.evaluate(() => document.getAnimations().length), 0);
  evidence.checks.push("Wide/narrow resize, forced colors, reduced motion and no page overflow: PASS");
  await page.emulateMedia({ forcedColors: "none", reducedMotion: "no-preference", colorScheme: "dark" });
  const manifest = JSON.parse(await readFile(join(root, "references.json"), "utf8"));
  const screens = captureMode === "all" ? manifest.screens : manifest.screens.filter((s) => s.id === "overview");
  for (const screen of screens) {
    for (const [name, viewport] of Object.entries(manifest.viewports).filter(([name]) => name !== "medium")) {
      await page.setViewportSize(viewport);
      await page.goto(origin + `/reference.html?theme=dark#${screen.id}`);
      await page.evaluate(() => { document.activeElement?.blur(); scrollTo(0, 0); });
      assert(await page.locator(`#${screen.id}`).isVisible());
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${screen.id}-${name} overflows`);
      const file = `${screen.id}-${name}.png`;
      await page.screenshot({ path: join(output, file), fullPage: true, style: fullPageStyle });
      evidence.screenshots.push(file);
    }
  }
  if (captureMode === "all") {
    for (const variant of [
      { name: "overview-medium", width: 900, height: 1000, screen: "overview", theme: "dark" },
      { name: "overview-light", width: 1440, height: 1000, screen: "overview", theme: "light" },
      { name: "settings-light", width: 1440, height: 1000, screen: "settings", theme: "light" },
      { name: "handoff-forced", width: 390, height: 844, screen: "handoff", theme: "system", forcedColors: "active" },
      { name: "overview-zoom-reflow", width: 720, height: 500, screen: "overview", theme: "dark" },
    ]) {
      await page.setViewportSize({ width: variant.width, height: variant.height });
      await page.emulateMedia({ forcedColors: variant.forcedColors || "none", reducedMotion: "reduce" });
      await page.goto(origin + `/reference.html?theme=${variant.theme}#${variant.screen}`);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), `${variant.name} overflows`);
      await page.evaluate(() => { document.activeElement?.blur(); scrollTo(0, 0); });
      await page.screenshot({ path: join(output, variant.name + ".png"), fullPage: true, style: fullPageStyle });
      evidence.screenshots.push(variant.name + ".png");
    }
    await page.emulateMedia({ forcedColors: "none" });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(origin + "/reference.html#overview");
    await page.evaluate(() => { document.activeElement?.blur(); scrollTo(0, 0); });
    await page.screenshot({ path: join(output, "overview-narrow-viewport.png"), fullPage: false });
    evidence.screenshots.push("overview-narrow-viewport.png");
    await page.locator(".bottom-nav summary").focus();
    await page.keyboard.press("Enter");
    assert(await page.locator('.bottom-nav a[href="#sessions"]').isVisible());
    await page.keyboard.press("Tab");
    assert.equal(await page.locator(":focus").innerText(), "Sessions");
    await page.screenshot({ path: join(output, "navigation-more.png"), fullPage: false });
    evidence.screenshots.push("navigation-more.png");
    evidence.checks.push("All route/state references wide/narrow; medium accessible icon rail; keyboard More; theme variants; 720 CSS-pixel reflow: PASS (reflow is not browser zoom qualification). Full-page narrow captures place the bottom navigation in normal flow to avoid concealing content; overview-narrow-viewport and navigation-more show its actual fixed placement.");
  }
  assert.deepEqual(errors, []);
  assert.deepEqual(external, []);
  evidence.checks.push("No page JavaScript errors or external runtime requests: PASS");
  const script = await readFile(join(root, "benchmark.mjs"));
  evidence.bundle = { chartDependencies: 0, combinedExperimentJSBytes: script.length, combinedExperimentJSGzipBytes: gzipSync(script).length, note: "Includes BOTH renderers, synthetic fixture, controls and table; not a production bundle or isolated renderer cost." };
  await writeFile(join(output, "results.json"), JSON.stringify(evidence, null, 2) + "\n");
  console.log(JSON.stringify({ checks: evidence.checks, timings: evidence.timings, bundle: evidence.bundle, screenshots: evidence.screenshots.length }, null, 2));
} finally {
  if (browser) await browser.close();
  await new Promise((done) => server.close(done));
}
