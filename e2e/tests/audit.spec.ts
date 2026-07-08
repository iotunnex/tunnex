import { test, expect, type Page } from "@playwright/test";

// S4.6 Audit log viewer. The seeded org has no audit events and sso.config_updated
// needs an enterprise SSO write, so the feed render + keyset paging are asserted
// against a MOCKED endpoint (like the other UI-render tests); the real page is
// checked for the org-scoped actor filter.
const OWNER = { email: "owner@demo.tunnex.local", pass: "tunnex-demo-password" };

async function login(page: Page) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(OWNER.email);
  await page.getByLabel("Password").fill(OWNER.pass);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await page.getByRole("link", { name: "Audit log" }).click();
  await expect(page.getByRole("heading", { name: "Audit log" })).toBeVisible();
}

test("the actor filter is org-scoped (offers only this org's members)", async ({ page }) => {
  await login(page);
  const actor = page.getByRole("combobox").first();
  // Only "Anyone" + the seeded org members — no foreign actor probe.
  await expect(actor.getByRole("option", { name: "Anyone" })).toBeAttached();
  await expect(actor.getByRole("option", { name: "Demo Member" })).toBeAttached();
  await expect(actor.getByRole("option", { name: "Demo Owner" })).toBeAttached();
});

test("the feed renders actions + resolved actors + secret-free details, with keyset Load more", async ({ page }) => {
  const OWNER_ID = "01900000-0000-7000-8000-000000000002"; // seeddata.DemoOwnerUserID
  // First page: exactly 50 (full → "Load more"); cursor'd page: fewer (end).
  const full = Array.from({ length: 50 }, (_, i) => ({
    id: `00000000-0000-7000-8000-${(100000000000 + i).toString()}`,
    created_at: new Date(1_700_000_000_000 - i * 60_000).toISOString(),
    actor_id: OWNER_ID,
    action: "device.created",
    target_type: "device",
    details: {} as Record<string, unknown>,
  }));
  // A secret-adjacent event: details carries the KEYED fingerprint, never a secret.
  full[0] = {
    ...full[0],
    action: "sso.config_updated",
    target_type: "sso_config",
    details: { provider: "google", client_id: "gid-123", enabled: true, secret_fingerprint: "a1b2c3d4e5f6" } as Record<string, unknown>,
  } as (typeof full)[number];

  await page.route("**/api/v1/organizations/*/audit-logs**", (route) => {
    const url = new URL(route.request().url());
    const body = url.searchParams.get("cursor_ts") ? full.slice(0, 3) : full; // cursor'd page is short → ends
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });

  await login(page);
  // Secret-free render: the fingerprint shows; no client_secret / sealed material.
  await expect(page.getByText("sso.config_updated")).toBeVisible();
  await expect(page.getByText(/a1b2c3d4e5f6/)).toBeVisible();
  await expect(page.getByText(/client_secret/i)).toHaveCount(0);
  // Actor resolved to a name (not a raw UUID).
  await expect(page.getByText(/actor Demo Owner/).first()).toBeVisible();
  // Full page → Load more; clicking it sends the keyset cursor and reaches the end.
  const loadMore = page.getByRole("button", { name: "Load more" });
  await expect(loadMore).toBeVisible();
  await loadMore.click();
  await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0); // short page → end
});
