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
    await page.evaluate(() => document.fonts.ready);
    await expect(page).toHaveScreenshot(`overview-${w.name}.png`, {
      fullPage: true,
      maxDiffPixelRatio: 0,
      animations: "disabled",
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
