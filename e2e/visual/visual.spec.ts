import { test, expect, type Page } from "@playwright/test";

// THE VIEWPORT LEG — the only instrument that answers "did anything move that nobody asked to move".
//
// ⛔ THE JUSTIFICATION IS A MEASUREMENT, NOT A PREFERENCE. All three visual defects of 2026-08-01 originated
// in SHARED CODE — a spacing config re-keyed px-vs-rem (128 use sites, 17 screens), a shared scale that
// rendered a donut at a quarter size, and a shared primitive whose `backdrop-filter` broke five modals.
// NONE originated in a screen. A screen-shaped suite pays per-screen maintenance to catch defects that are
// not screen-shaped, and is re-baselined every time a screen is redesigned — twelve more times this epic.
//
// So: a PRIMITIVES GALLERY (the shared surface) plus ONE real screen (the only one redesigned so far).
//
// ── WHAT THIS CANNOT SEE, stated here rather than discovered later ──────────────────────────────────────
//   · whether the design is RIGHT — a diff cannot want something. Only the founder's review answers that.
//   · any state not captured. Coverage is the enumerated states and nothing else.
//   · contrast/readability in situ; the token gate computes ratios on pairs, not text over a gradient.
//   · the eleven screens still unbuilt — each takes its snapshot at the end of its own section.
//   · browsers other than chromium.
//   · motion, which is frozen to make this deterministic.

const WIDTHS = [
  // The design's native width. Every one of the three defects above would have fired here.
  { name: "1440", width: 1440, height: 1000 },
  // The narrow rearrangement — drawer nav, triage bar, ComposeGate absence. This layout is OURS (the
  // prototype is desktop-only, min-width 1280), so it has no other reviewer.
  { name: "390", width: 390, height: 900 },
];

/**
 * DETERMINISM. A flaky visual suite gets rubber-stamped no matter how it is governed, so every known source
 * of run-to-run variation is removed rather than tolerated.
 */
async function stabilise(page: Page) {
  // `relativeAge` renders "3s ago" / "12m ago" on four screens — the single largest source of false diffs.
  await page.clock.setFixedTime(new Date("2026-08-01T12:00:00Z"));
  // The token CSS already zeroes every duration under this query; this makes the browser assert it.
  await page.emulateMedia({ reducedMotion: "reduce" });
}

test.describe("visual — the shared surface", () => {
  for (const w of WIDTHS) {
    test(`primitives gallery @ ${w.name}`, async ({ page }) => {
      await page.setViewportSize({ width: w.width, height: w.height });
      await stabilise(page);
      await page.goto("/__visual");
      await expect(page.locator("[data-visual-gallery]")).toBeVisible();
      // Fonts must be loaded before the shot or glyph metrics shift mid-capture.
      await page.evaluate(() => document.fonts.ready);
      await expect(page).toHaveScreenshot(`gallery-${w.name}.png`, {
        fullPage: true,
        maxDiffPixelRatio: 0,
        animations: "disabled",
      });
    });
  }
});

// ⛔ THE WIDTH-SENSITIVE CLASS, CAPTURED SEPARATELY AND AT 1440 ONLY (founder-ruled 2026-08-02).
//
// SEPARATE, not appended to the gallery image: diff legibility is the point of a small suite. Appending
// would roughly double the page and spread any change across more pixels — making a real regression harder
// to see in the very image a human is meant to read. Every image must EARN ITS PLACE, and one that isolates
// the class where a component's geometry derives from its CONTAINER earns it.
//
// ⛔ 1440 ONLY, AND THAT IS NOT AN OVERSIGHT. At 390 there is no wide column, so a "wide specimen" is just
// the narrow one again — it would test nothing while costing a baseline and a re-harvest on every change.
// This note exists so nobody adds `gallery-wide-390.png` for symmetry.
test.describe("width-sensitive specimens at full column width", () => {
  test("wide specimens @ 1440", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 1200 });
    await stabilise(page);
    await page.goto("/__visual");
    const wide = page.locator("[data-wide-specimens]");
    await expect(wide).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    await expect(wide).toHaveScreenshot("gallery-wide-1440.png", {
      maxDiffPixelRatio: 0,
      animations: "disabled",
    });
  });
});

// ⛔ NO HORIZONTAL OVERFLOW, AT ANY CAPTURED WIDTH.
//
// This assertion exists because the FIRST baseline run produced a 455px-wide capture from a 390px viewport —
// the page scrolled sideways, and the image would have BAKED THE DEFECT IN as the expected appearance.
//
// A snapshot cannot catch this on its own: it records what the page looks like, including looking wrong. So
// the geometric invariant is asserted SEPARATELY and in numbers, where a human does not have to notice a
// 65px difference between two images they have never seen before.
test.describe("no page scrolls sideways", () => {
  for (const w of WIDTHS) {
    test(`gallery has no horizontal overflow @ ${w.name}`, async ({ page }) => {
      await page.setViewportSize({ width: w.width, height: w.height });
      await stabilise(page);
      await page.goto("/__visual");
      await expect(page.locator("[data-visual-gallery]")).toBeVisible();
      const overflow = await page.evaluate(
        () =>
          document.documentElement.scrollWidth -
          document.documentElement.clientWidth,
      );
      expect(
        overflow,
        `the page is ${overflow}px wider than the viewport`,
      ).toBeLessThanOrEqual(0);
    });
  }
});
