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

// S4.5b — the address-pool resize control. The seeded org has the default pool
// (10.99.0.0/24). Real render is checked directly; the success (accept-and-
// surface) and the shrink-conflict (orphan list) renders are mocked — the
// orphan list needs stranded devices, which need an enrolled gateway the e2e
// stack doesn't have, so we mock the endpoint like the other UI-render tests.
test("the address-pool section shows the current CIDR and gates Resize on a change", async ({ page }) => {
  await login(page, OWNER);
  await expect(page.getByText("Address pool")).toBeVisible();
  await expect(page.getByLabel("Pool CIDR")).toHaveValue("10.99.0.0/24");
  await expect(page.getByRole("button", { name: "Resize pool" })).toBeDisabled(); // unchanged
});

test("a successful resize surfaces the re-issue-configs consequence (accept-and-surface)", async ({ page }) => {
  await page.route("**/api/v1/organizations/*/pool-cidr", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        id: ORG, name: "Demo Organization", slug: "demo", pool_cidr: "10.99.0.0/23",
        created_at: new Date(0).toISOString(), updated_at: new Date(0).toISOString(),
      }),
    }),
  );
  await login(page, OWNER);
  await page.getByLabel("Pool CIDR").fill("10.99.0.0/23");
  await page.getByRole("button", { name: "Resize pool" }).click();
  await expect(page.getByText(/re-issue their configs/i)).toBeVisible();
  await expect(page.getByText(/revoke \+ recreate/i)).toBeVisible();
});

test("a shrink that would strand devices renders the orphan list with names and reasons", async ({ page }) => {
  await page.route("**/api/v1/organizations/*/pool-cidr", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        orphan_count: 3,
        orphans: [
          { device_id: "01900000-0000-7000-8000-0000000000a1", name: "laptop-a", assigned_ip: "10.99.0.127", reason: "reserved_collision" },
          { device_id: "01900000-0000-7000-8000-0000000000a2", name: "laptop-b", assigned_ip: "10.99.0.200", reason: "out_of_range" },
        ],
      }),
    }),
  );
  await login(page, OWNER);
  await page.getByLabel("Pool CIDR").fill("10.99.0.0/25");
  await page.getByRole("button", { name: "Resize pool" }).click();
  // Actionable refusal: count, names, both reason phrasings, and the "N more" note.
  await expect(page.getByText(/3 devices must be removed or renumbered first/i)).toBeVisible();
  await expect(page.getByText("laptop-a")).toBeVisible();
  await expect(page.getByText("laptop-b")).toBeVisible();
  await expect(page.getByText(/collides with a reserved address/i)).toBeVisible();
  await expect(page.getByText(/outside the new range/i)).toBeVisible();
  await expect(page.getByText(/and 1 more/i)).toBeVisible();
});
