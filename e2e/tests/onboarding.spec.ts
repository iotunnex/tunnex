import { test, expect, type Page } from "@playwright/test";

// S4.7 Fresh-user onboarding funnel. The e2e stack is the OPEN edition. The seeded
// users all already belong to the demo org, so the zero-org branches (create-org,
// verify-pending, invitation-only) are driven by MOCKING GET /organizations to
// return [] for the logged-in user — the same UI-render convention the audit /
// settings specs use. The has-org happy path and the enroll ceremony run against
// the REAL backend.
const OWNER = { email: "owner@demo.tunnex.local", pass: "tunnex-demo-password" }; // verified
const UNVERIFIED = { email: "unverified-admin@demo.tunnex.local", pass: "tunnex-demo-password" };
const ORG = "01900000-0000-7000-8000-000000000001"; // seeddata.DemoOrgID

// Matches …/api/v1/organizations exactly (optional query) — NOT the sub-resource
// paths like …/organizations/{id}/overview, so those still hit the real backend.
const ORGS_URL = /\/api\/v1\/organizations(\?.*)?$/;

// Raw sign-in that does NOT assert it lands on Overview — a zero-org user is
// bounced into the funnel instead of the dashboard.
async function signIn(page: Page, who: { email: string; pass: string }) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(who.email);
  await page.getByLabel("Password").fill(who.pass);
  await page.getByRole("button", { name: "Sign in" }).click();
}

test("a verified user with no organization is routed to the create-org step", async ({ page }) => {
  await page.route(ORGS_URL, (route) => route.fulfill({ status: 200, contentType: "application/json", body: "[]" }));
  await signIn(page, OWNER);
  await expect(page.getByRole("heading", { name: "Create your organization" })).toBeVisible();
  await expect(page).toHaveURL(/\/create-org$/);

  // A manually-typed hyphenated slug must survive left-to-right typing — the
  // sanitizer must not strip a trailing hyphen mid-word (else "acme-corp" would
  // collapse to "acmecorp"). pressSequentially fires one onChange per key.
  const slug = page.getByLabel("Slug");
  await slug.click();
  await slug.pressSequentially("acme-corp");
  await expect(slug).toHaveValue("acme-corp");
});

test("an unverified user with no organization is routed to verify-pending, not create-org", async ({ page }) => {
  await page.route(ORGS_URL, (route) => route.fulfill({ status: 200, contentType: "application/json", body: "[]" }));
  await signIn(page, UNVERIFIED);
  // Create-org is verified-gated server-side, so the funnel sends them to verify
  // FIRST (structural refusal, not a surprise 403 after filling the form).
  await expect(page.getByRole("heading", { name: "Verify your email" })).toBeVisible();
  await expect(page.getByText(UNVERIFIED.email)).toBeVisible();
  await expect(page).toHaveURL(/\/verify-pending$/);
  await expect(page.getByRole("heading", { name: "Create your organization" })).toHaveCount(0);
});

test("a user who already has an organization skips the funnel and lands on the dashboard", async ({ page }) => {
  // No mock — the real demo org membership must send the owner straight to the shell.
  await signIn(page, OWNER);
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole("heading", { name: "Create your organization" })).toHaveCount(0);
});

test("the create-org step surfaces the single-org cap as an invitation-only message", async ({ page }) => {
  // 0 orgs → routed to create-org; the server owns the cap and refuses the POST
  // with org_limit_reached; the UI mirrors that (never invents the permission).
  await page.route(ORGS_URL, (route) => {
    if (route.request().method() === "POST") {
      return route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "org_limit_reached", message: "single organization only" } }),
      });
    }
    return route.fulfill({ status: 200, contentType: "application/json", body: "[]" });
  });
  await signIn(page, OWNER);
  await expect(page.getByRole("heading", { name: "Create your organization" })).toBeVisible();
  await page.getByLabel("Organization name").fill("Second Org");
  await page.getByRole("button", { name: "Create organization" }).click();
  await expect(page.getByRole("heading", { name: "Invitation required" })).toBeVisible();
});

test("a successful create routes the fresh user into the dashboard", async ({ page }) => {
  const ORG_OBJ = {
    id: ORG,
    name: "Funnel Org",
    slug: "funnel-org",
    pool_cidr: "10.99.0.0/24",
    created_at: new Date(0).toISOString(),
    updated_at: new Date(0).toISOString(),
  };
  // Stateful: the list is empty until the org is created, then contains it — so
  // RequireOrg funnels first, then (after the POST) admits the user to the shell.
  let created = false;
  await page.route(ORGS_URL, (route) => {
    if (route.request().method() === "POST") {
      created = true;
      return route.fulfill({ status: 201, contentType: "application/json", body: JSON.stringify(ORG_OBJ) });
    }
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(created ? [ORG_OBJ] : []) });
  });
  await page.route(/\/api\/v1\/organizations\/[^/]+\/overview$/, (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ members: 1, devices: 0, nodes: 0, online: 0, recent_activity: [] }),
    }),
  );
  await signIn(page, OWNER);
  await expect(page.getByRole("heading", { name: "Create your organization" })).toBeVisible();
  await page.getByLabel("Organization name").fill("Funnel Org");
  await expect(page.getByLabel("Slug")).toHaveValue("funnel-org"); // auto-derived
  await page.getByRole("button", { name: "Create organization" }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(page.getByText("Funnel Org")).toBeVisible();
  await expect(page).toHaveURL(/\/dashboard$/);
});

test("enrolling a gateway shows the join token exactly once (one-time-secret ceremony)", async ({ page }) => {
  const TOKEN = "jt-onboarding-secret-xyz";
  let issued = 0;
  await page.route("**/api/v1/organizations/*/nodes/join-token", (route) => {
    issued++;
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ join_token: TOKEN }) });
  });
  // Owner has the real demo org → straight to the shell, then to Devices where the
  // Gateways enroll ceremony lives.
  await signIn(page, OWNER);
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await page.getByRole("link", { name: "Devices" }).click();
  await expect(page.getByRole("heading", { name: "Gateways" })).toBeVisible();

  await page.getByRole("button", { name: "Enroll gateway" }).click();
  await page.getByRole("button", { name: "Generate join token" }).click();

  // The one-time ceremony: amber modal, token shown, must be acknowledged.
  await expect(page.getByText("Join token — shown once")).toBeVisible();
  await expect(page.getByText(new RegExp(TOKEN))).toBeVisible();
  await page.getByRole("button", { name: /I.?ve saved it/ }).click();
  // Dismissed → the token is gone from the page and was minted exactly once (it is
  // never re-served; re-enrolling would mint a NEW token).
  await expect(page.getByText(new RegExp(TOKEN))).toHaveCount(0);
  expect(issued).toBe(1);
});
