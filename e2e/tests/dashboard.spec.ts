import { test, expect, type Page } from "@playwright/test";

// S4.3 dashboard home. The seeded demo org has one member (the owner) and no
// gateways, devices, or activity yet — so it exercises BOTH the real-count path
// (Members renders a live number) and the empty-state onboarding funnel.
const OWNER_EMAIL = "owner@demo.tunnex.local";
const OWNER_PASS = "tunnex-demo-password";

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(OWNER_EMAIL);
  await page.getByLabel("Password").fill(OWNER_PASS);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
}

test("dashboard renders real counts for the seeded org", async ({ page }) => {
  await login(page);
  // (Org name is asserted in settings.spec, not here — the settings edit test
  // renames+reverts the shared demo org, so an exact-name check would race it.)
  // The Members stat is a live count: the label sits directly under its value.
  const membersLabel = page.getByText("Members", { exact: true });
  await expect(membersLabel).toBeVisible();
  await expect(membersLabel.locator("xpath=preceding-sibling::div[1]")).toHaveText(/^\d+$/);
  // Honest online label from S3.6 (not a fabricated "online" claim).
  await expect(page.getByText("Seen in last 3 min")).toBeVisible();
});

test("a fresh org shows the empty-state onboarding funnel", async ({ page }) => {
  await login(page);
  // No gateway is enrolled in the seed, so the onboarding call-to-enroll shows.
  // This is the durable empty-state: nothing in the test suite ever enrolls a
  // real node. (We deliberately do NOT assert "No activity yet" here — audit
  // activity legitimately accumulates as other tests invite/change roles, and
  // audit_logs is append-only. The empty activity RENDER is covered
  // deterministically below with a mocked overview.)
  await expect(page.getByText("No gateway enrolled yet.")).toBeVisible();
});

test("the activity feed renders an explicit empty state (mocked)", async ({ page }) => {
  // Deterministic empty-activity render, independent of accumulated audit rows.
  await page.route("**/api/v1/organizations/*/overview", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ members: 1, devices: 0, nodes: 0, online: 0, recent_activity: [] }),
    }),
  );
  await login(page);
  await expect(page.getByText("No activity yet.")).toBeVisible();
});

test("the reserved status-green appears only on the live liveness tile", async ({ page }) => {
  // Mock a populated overview so the online tile is > 0. The seed has online=0,
  // so this is the only way to exercise the reservation. Green (#2ecc8f / .text-ok)
  // must land on the 'Seen in last 3 min' value and nowhere else on the page.
  await page.route("**/api/v1/organizations/*/overview", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ members: 4, devices: 3, nodes: 1, online: 2, recent_activity: [] }),
    }),
  );
  await login(page);

  const onlineValue = page.getByText("Seen in last 3 min").locator("xpath=preceding-sibling::div[1]");
  await expect(onlineValue).toHaveText("2");
  await expect(onlineValue).toHaveClass(/text-ok/);
  // The other tiles are neutral, never green — green is a status color, not brand.
  const membersValue = page.getByText("Members", { exact: true }).locator("xpath=preceding-sibling::div[1]");
  await expect(membersValue).not.toHaveClass(/text-ok/);
});
