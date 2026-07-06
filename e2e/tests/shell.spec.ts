import { test, expect } from "@playwright/test";

// S4.1: the auth-gated app shell. Requires the demo org/owner to be seeded
// (make seed) — owner@demo.tunnex.local / tunnex-demo-password.
const OWNER_EMAIL = "owner@demo.tunnex.local";
const OWNER_PASS = "tunnex-demo-password";

test("unauthenticated visitors are gated to the login screen", async ({ page }) => {
  await page.goto("/devices"); // deep link into a gated route
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
});

test("signing in reaches the app shell and the dashboard, then navigates to devices", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill(OWNER_EMAIL);
  await page.getByLabel("Password").fill(OWNER_PASS);
  await page.getByRole("button", { name: "Sign in" }).click();

  // Landed in the authenticated shell on the dashboard (the default authed route).
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Log out" })).toBeVisible();
  // The owner's email shows in the header.
  await expect(page.getByText(OWNER_EMAIL)).toBeVisible();

  // The sidebar links to Devices; the create-device form lives there.
  await page.getByRole("link", { name: "Devices" }).click();
  await expect(page.getByRole("heading", { name: "Devices" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Create device" })).toBeVisible();

  // An authenticated user visiting /login is bounced back into the app (AnonOnly).
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();

  // Logging out returns to the login screen.
  await page.getByRole("button", { name: "Log out" }).click();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
});
