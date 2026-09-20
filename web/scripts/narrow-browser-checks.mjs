/* global document, window */
import assert from "node:assert/strict";

export async function testSettingsNarrowReflow(page, check) {
  await page.setViewportSize({ width: 320, height: 844 });
  await page.evaluate(
    () =>
      new Promise((resolve) =>
        window.requestAnimationFrame(() => window.requestAnimationFrame(resolve)),
      ),
  );

  const measurements = await page
    .locator('main input[type="file"]')
    .first()
    .evaluate((input) => {
      const bounds = input.getBoundingClientRect();
      return {
        documentWidth: document.documentElement.scrollWidth,
        viewportWidth: document.documentElement.clientWidth,
        inputLeft: bounds.left,
        inputRight: bounds.right,
      };
    });
  assert.ok(
    measurements.documentWidth <= measurements.viewportWidth,
    `Settings document width ${measurements.documentWidth}px exceeds its ${measurements.viewportWidth}px viewport`,
  );
  assert.ok(
    measurements.inputLeft >= 0 && measurements.inputRight <= measurements.viewportWidth,
    `configuration file input spans ${measurements.inputLeft}px–${measurements.inputRight}px in a ${measurements.viewportWidth}px viewport`,
  );
  check("Settings configuration transfer reflows without horizontal overflow at 320px");
}

export async function testNarrowFocusVisibility(page, check) {
  await page.setViewportSize({ width: 390, height: 844 });
  const edit = page.getByRole("button", { name: "Edit", exact: true }).filter({ visible: true });
  await edit.evaluate((target) => {
    const controls = [
      ...document.querySelectorAll(
        'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])',
      ),
    ].filter((element) => element.getClientRects().length > 0);
    const index = controls.indexOf(target);
    if (index <= 0) throw new Error("Edit needs a preceding keyboard control");
    controls[index - 1].focus();
  });
  await edit.evaluate((target) => {
    const navigation = document.querySelector('nav[aria-label="Primary narrow"]');
    if (!navigation) throw new Error("narrow navigation is missing");
    const targetBounds = target.getBoundingClientRect();
    const navigationBounds = navigation.getBoundingClientRect();
    window.scrollBy(0, targetBounds.top - navigationBounds.top - 4);
  });

  assert.equal(
    await edit.evaluate((target) => {
      const navigation = document.querySelector('nav[aria-label="Primary narrow"]');
      if (!navigation) throw new Error("narrow navigation is missing");
      return target.getBoundingClientRect().bottom > navigation.getBoundingClientRect().top;
    }),
    true,
    "test setup must position Edit beneath the fixed narrow navigation",
  );
  await page.keyboard.press("Tab");

  const measurements = await page.evaluate(() => {
    const target = document.activeElement?.getBoundingClientRect();
    const navigation = document
      .querySelector('nav[aria-label="Primary narrow"]')
      ?.getBoundingClientRect();
    return target && navigation
      ? {
          targetTop: target.top,
          targetBottom: target.bottom,
          navigationTop: navigation.top,
          focusedLabel: document.activeElement?.textContent?.trim(),
        }
      : null;
  });
  assert.ok(measurements, "focused Edit control and narrow navigation must be measurable");
  assert.equal(measurements.focusedLabel, "Edit", "Tab must move keyboard focus to Edit");
  assert.ok(
    measurements.targetTop >= 0 && measurements.targetBottom <= measurements.navigationTop,
    `focused Edit control spans ${measurements.targetTop}px–${measurements.targetBottom}px beneath navigation starting at ${measurements.navigationTop}px`,
  );
  check("Profiles keeps its focused Edit control visible above narrow navigation");
}
