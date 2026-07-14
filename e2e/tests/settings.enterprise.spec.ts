import { test, expect, type Page } from "@playwright/test";

// S4.5 + S4.5b — the REAL assertions, run ONLY against the enterprise e2e stack
// (E2E_EDITION=enterprise) which has seed-enterprise laid down on top of seed:
// a sealed SSO config (google) + a device holding a pool IP a shrink strands.
//
// These SATISFY what the open-edition substitutes in settings.spec.ts can only
// stand in for:
//   - S4.5: there the SSO section is the "Enterprise feature" 403 gate; HERE the
//     endpoint is served, so we assert the real payload carries the fingerprint
//     and NO client secret (belt-and-braces with the blocking Go httptest
//     TestGetSsoConfigPayloadCarriesNoSecret, which gates in `make test-editions`).
//   - S4.5b: there the 409 orphan list is a page.route MOCK; HERE a live shrink
//     strands the seeded device and the REAL 409 body renders.
const ENTERPRISE = process.env.E2E_EDITION === "enterprise";

const OWNER = { email: "owner@demo.tunnex.local", pass: "tunnex-demo-password" };
const ORG = "01900000-0000-7000-8000-000000000001"; // seeddata.DemoOrgID
// seed-enterprise fixtures (must match apps/api/internal/seeddata/seeddata.go).
const SEEDED_CLIENT_ID = "demo-enterprise-client.apps.googleusercontent.com";
const SEEDED_SECRET = "demo-enterprise-client-secret-do-not-ship";
const STRANDABLE_DEVICE = "demo-strandable-laptop";
const SHRINK_CIDR = "10.99.0.0/25"; // strands the seeded device at 10.99.0.200

async function login(page: Page, who: { email: string; pass: string }) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(who.email);
  await page.getByLabel("Password").fill(who.pass);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await page.getByRole("link", { name: "Settings" }).click();
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
}

test("S4.5 — the SSO config payload surfaces the fingerprint and NO client secret", async ({ page }) => {
  test.skip(!ENTERPRISE, "requires the enterprise e2e stack (E2E_EDITION=enterprise)");
  await login(page, OWNER);

  // The enterprise edition serves the real SSO config form (not the gate note).
  await expect(page.getByText(/Tunnex Enterprise feature/i)).toHaveCount(0);
  // The seeded google config renders its client id and the proof-of-storage
  // fingerprint — and NOTHING that is or contains the secret. Providers render in
  // order [google, microsoft], so the first Client ID field is google's.
  await expect(page.getByLabel("Client ID").first()).toHaveValue(SEEDED_CLIENT_ID);
  await expect(page.getByText(/stored secret fingerprint:/i)).toBeVisible();
  await expect(page.getByText(SEEDED_SECRET)).toHaveCount(0);

  // Assert the wire payload directly (authenticated via the logged-in context).
  const res = await page.request.get(`/api/v1/organizations/${ORG}/sso/google`);
  expect(res.status()).toBe(200);
  const body = await res.json();
  const rawText = await res.text();
  expect(body.secret_fingerprint, "fingerprint present").toBeTruthy();
  expect(body.client_id).toBe(SEEDED_CLIENT_ID);
  expect(body).not.toHaveProperty("client_secret");
  expect(rawText, "no secret material anywhere in the payload").not.toContain(SEEDED_SECRET);
});

test("S4.5b — a live shrink strands the seeded device and renders the REAL 409 orphan list", async ({ page }) => {
  test.skip(!ENTERPRISE, "requires the enterprise e2e stack (E2E_EDITION=enterprise)");
  await login(page, OWNER);

  await expect(page.getByText("Address pool")).toBeVisible();
  await expect(page.getByLabel("Pool CIDR")).toHaveValue("10.99.0.0/24");

  // No page.route mock — this hits the real endpoint. The seeded device at
  // 10.99.0.200 is outside 10.99.0.0/25, so the server returns a real 409.
  await page.getByLabel("Pool CIDR").fill(SHRINK_CIDR);
  await page.getByRole("button", { name: "Resize pool" }).click();

  await expect(page.getByText(/must be removed or renumbered/i)).toBeVisible();
  await expect(page.getByText(STRANDABLE_DEVICE)).toBeVisible();
  await expect(page.getByText(/outside the new range/i)).toBeVisible();

  // The 409 is non-mutating, so the pool is unchanged — nothing to restore. Prove it.
  const res = await page.request.get(`/api/v1/organizations/${ORG}`);
  expect((await res.json()).pool_cidr).toBe("10.99.0.0/24");
});
