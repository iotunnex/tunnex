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

// ⛔ CHART HEIGHT IS BOUNDED, AND THIS ASSERTION EXISTS BECAUSE THE IMAGE THAT CAUGHT IT IS NOW ADVISORY.
//
// TWO PRIMITIVES SHIPPED WITH THE SAME DEFECT: an SVG with a `viewBox` and `w-full` but NO HEIGHT derives its
// height from its WIDTH. `NodeLink` rendered ~750px tall in an 8fr column; `AreaChart` rendered ~500px — in
// the primitive written AFTER the law about it was filed.
//
// The wide pixel specimen caught the second one on its first render. That specimen is now `continue-on-error`,
// so the class would have lost its only detector. THIS IS THE SAME COVERAGE AS A NUMBER RATHER THAN A PICTURE:
// deterministic, no baseline, no harvest, and it fails with a height in the message instead of a diff to
// adjudicate.
//
// A DEMOTED CHECK MUST HAVE ITS FINDINGS RE-HOMED, NOT JUST ITS RED SUPPRESSED. Otherwise "advisory" quietly
// means "the class it caught is now uncovered".
test.describe("chart primitives are bounded in height", () => {
  test("no chart renders taller than its design allows @ 1440", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 1440, height: 1200 });
    await stabilise(page);
    await page.goto("/__visual");
    await expect(page.locator("[data-visual-gallery]")).toBeVisible();

    // Every viz primitive renders through `VizFrame`, which emits a labelled <figure>. One query reaches all
    // of them, including any added later — an enumeration rather than a list of the three that exist today.
    const figures = page.locator("figure[aria-label]");
    const n = await figures.count();
    expect(n, "no charts found — the query is wrong, not the page").toBeGreaterThan(2);

    for (let i = 0; i < n; i++) {
      const fig = figures.nth(i);
      const label = await fig.getAttribute("aria-label");
      const box = await fig.boundingBox();
      expect(box, `${label} has no box`).not.toBeNull();
      // 420px: the tallest legitimate chart here is the 320px node-link plus its legend row. A width-derived
      // height lands at 500-750px, so the bound separates the two cases without being fussy about layout.
      expect(
        box!.height,
        `chart "${label}" is ${Math.round(box!.height)}px tall — a viewBox with w-full and no height derives its height from its WIDTH`,
      ).toBeLessThanOrEqual(420);
      expect(box!.height, `chart "${label}" collapsed`).toBeGreaterThan(20);
    }
  });
});
