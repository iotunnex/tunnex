import { test, expect, type Page } from "@playwright/test";

// S4.5 Org settings + SSO config UI. The e2e stack is the OPEN edition, so the
// SSO section renders as an "Enterprise feature" note (watch-item b: SSO config
// is hidden in open builds), and the org-name edit exercises the settings save.
const OWNER = { email: "owner@demo.tunnex.local", pass: "tunnex-demo-password" };
const MEMBER = { email: "member@demo.tunnex.local", pass: "tunnex-demo-password" };
const ORG = "01900000-0000-7000-8000-000000000001"; // seeddata.DemoOrgID

async function login(page: Page, who: { email: string; pass: string }) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(who.email);
  await page.getByLabel("Password").fill(who.pass);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await page.getByRole("link", { name: "Settings" }).click();
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
}

test("owner sees org settings; SSO config is gated to the enterprise edition", async ({ page }) => {
  await login(page, OWNER);
  await expect(page.getByText("Organization", { exact: true })).toBeVisible();
  await expect(page.getByText("slug: demo")).toBeVisible();
  // Open edition: no SSO config form, just the enterprise note (watch-item b).
  await expect(page.getByText(/Tunnex Enterprise feature/i)).toBeVisible();
  await expect(page.getByLabel("Client ID")).toHaveCount(0);
});

test("a plain member cannot manage settings", async ({ page }) => {
  await login(page, MEMBER);
  await expect(page.getByText(/managed by owners and admins/i)).toBeVisible();
  await expect(page.getByLabel("Name")).toHaveCount(0);
});

test("editing the org name saves (and is reverted to keep the shared seed clean)", async ({ page }) => {
  await login(page, OWNER);
  const name = page.getByLabel("Name");
  await expect(name).toHaveValue("Demo Organization");
  // Save is disabled until the name actually changes.
  await expect(page.getByRole("button", { name: "Save" })).toBeDisabled();
  try {
    await name.fill("Demo Organization (edited)");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Saved.")).toBeVisible();
  } finally {
    // ALWAYS restore the name via the API so a mid-test failure can't leave the
    // shared demo org renamed for other specs.
    await page.request.patch(`/api/v1/organizations/${ORG}`, {
      headers: { "X-Tunnex-CSRF": "1" },
      data: { name: "Demo Organization" },
    });
  }
});
