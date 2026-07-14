import { test, expect } from "@playwright/test";
import { login, OWNER, MEMBER, ORG } from "./helpers";

// S4.5 Org settings + SSO config UI. The e2e stack is the OPEN edition, so the
// SSO section renders as an "Enterprise feature" note (watch-item b: SSO config
// is hidden in open builds), and the org-name edit exercises the settings save.
//
// NOTE (S7.4c): the SSO test below is the OPEN-edition SUBSTITUTE — it proves the
// endpoint is edition-GATED (403), not that its served payload is secret-free.
// The REAL S4.5 secret-payload assertion lives in settings.enterprise.spec.ts
// (enterprise edition, self-detected via /meta) + the blocking Go httptest
// TestGetSsoConfigPayloadCarriesNoSecret (make test-editions).

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
// surface) and the shrink-conflict (orphan list) renders are mocked here in the
// OPEN suite because it has no seeded device to strand.
//
// NOTE (S7.4c): the 409-orphan MOCK below is the OPEN-edition SUBSTITUTE. The
// REAL S4.5b assertion — a LIVE shrink stranding a seeded device, un-mocked —
// lives in settings.enterprise.spec.ts (E2E_EDITION=enterprise), where
// seed-enterprise provides the device holding a pool IP. (The orphan check is a
// pure DB read — no enrolled agent needed; S7.4c D-c4.)
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
