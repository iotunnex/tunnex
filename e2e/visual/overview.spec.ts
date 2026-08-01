import { test, expect, type Page } from "@playwright/test";
import { OWNER } from "../tests/helpers";

// Overview is the ONLY redesigned screen, so it is the only real screen with a baseline. The other eleven
// take theirs as the last step of their own section — a baseline captured for a screen about to be redesigned
// is a baseline that will be discarded unread.

const WIDTHS = [
  { name: "1440", width: 1440, height: 1200 },
  { name: "390", width: 390, height: 1200 },
];

async function stabilise(page: Page) {
  await page.clock.setFixedTime(new Date("2026-08-01T12:00:00Z"));
  await page.emulateMedia({ reducedMotion: "reduce" });
}

for (const w of WIDTHS) {
  test(`overview @ ${w.name}`, async ({ page }) => {
    await page.setViewportSize({ width: w.width, height: w.height });
    await stabilise(page);
    // A local sign-in rather than the shared helper: that one navigates on to Settings, and this snapshot
    // must be of Overview. Reusing it would have captured the wrong page and the baseline would have looked
    // plausible.
    await page.goto("/login");
    await page.getByLabel("Email").fill(OWNER.email);
    await page.getByLabel("Password").fill(OWNER.pass);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
    // Wait for the independently-resolving cards, or the shot races the slowest fetch.
    await expect(page.getByRole("group", { name: "Members" })).toBeVisible();
    // ⛔ WAIT FOR ASYNC STATE TO SETTLE, DO NOT MASK IT.
    //
    // `HealthStatus` renders "checking…" then flips to "operational" when /healthz answers. The screenshot
    // raced that transition: the diff was 621 pixels confined to a single 40px band at y 921–960, which is
    // exactly where it sits. A component that CHANGES is not a component that is volatile — the settled state
    // is deterministic and worth asserting, so this waits for it rather than excluding it.
    //
    // Masking here would have hidden a real surface. The distinction: mask what CANNOT be made deterministic
    // (a wall-clock age); WAIT for what merely has not settled yet.
    await expect(page.getByText(/control plane operational/)).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    await expect(page).toHaveScreenshot(`overview-${w.name}.png`, {
      fullPage: true,
      maxDiffPixelRatio: 0,
      animations: "disabled",
      // ⛔ MASK WALL-CLOCK-DERIVED TEXT, DO NOT WIDEN THE THRESHOLD.
      //
      // Freezing the browser clock fixes a variable NOW; it does nothing about variable DATA. The seed writes
      // its audit rows at SEED time, which differs every CI run, so "2m ago" becomes "5m ago" and the image
      // diverges by ~118px forever. The first instinct is maxDiffPixelRatio — and a threshold is exactly how
      // a visual suite stops meaning anything, because it then also passes the real regressions beneath it.
      //
      // Masking is narrower and honest: this snapshot covers LAYOUT, and the timestamp VALUE is unit-tested
      // in `relativeAge`. What is excluded is named in the markup (`data-volatile`), not hidden in a number.
      mask: [page.locator("[data-volatile]")],
    });
  });
}

// The same geometric invariant on the real screen — this is where it actually fired.
for (const w of WIDTHS) {
  test(`overview has no horizontal overflow @ ${w.name}`, async ({ page }) => {
    await page.setViewportSize({ width: w.width, height: w.height });
    await stabilise(page);
    await page.goto("/login");
    await page.getByLabel("Email").fill(OWNER.email);
    await page.getByLabel("Password").fill(OWNER.pass);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
    const overflow = await page.evaluate(
      () =>
        document.documentElement.scrollWidth -
        document.documentElement.clientWidth,
    );
    expect(
      overflow,
      `Overview is ${overflow}px wider than the ${w.name}px viewport`,
    ).toBeLessThanOrEqual(0);
  });
}
