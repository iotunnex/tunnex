import { test, expect, type Page } from "@playwright/test";

// S4.7 Fresh-user onboarding funnel. The e2e stack is the OPEN edition. The seeded
// users all already belong to the demo org, so the zero-org branches (create-org,
// verify-pending, invitation-only) are driven by MOCKING GET /organizations to
// return [] for the logged-in user — the same UI-render convention the audit /
// settings specs use. The has-org happy path and the enroll ceremony run against
// the REAL backend.
const OWNER = { email: "owner@demo.tunnex.local", pass: "tunnex-demo-password" }; // verified, has demo org
// A VERIFIED user with NO membership (seeddata.DemoNoOrgUser) — the real fresh-
// signup state. Because the open-edition single-org slot is already taken by the
// demo org, this user's create attempt is refused by the REAL backend, so the
// routing and the invitation-only cap are proven end-to-end, not mocked.
const FRESH = { email: "fresh-user@demo.tunnex.local", pass: "tunnex-demo-password" };
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

test("a verified user with no organization is routed to the create-org step (real backend)", async ({ page }) => {
  // No mock — the real fresh user has zero memberships, so RequireOrg funnels them.
  await signIn(page, FRESH);
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

test("the open-build second-signup path ends on the invitation card with no usable create affordance (real backend)", async ({ page }) => {
  // REAL open-edition proof: the fresh verified 0-membership user is routed to
  // create-org, but the deployment's single-org slot is taken by the demo org, so
  // the REAL POST returns org_limit_reached and the UI lands on the invitation-only
  // dead-end — with the create form (and any create control) gone.
  await signIn(page, FRESH);
  await expect(page.getByRole("heading", { name: "Create your organization" })).toBeVisible();
  await page.getByLabel("Organization name").fill("Second Org");
  await page.getByRole("button", { name: "Create organization" }).click();
  await expect(page.getByRole("heading", { name: "Invitation required" })).toBeVisible();
  // No usable create affordance survives: the form, its fields, and the submit
  // button are all gone — the user cannot attempt a create from the dead-end.
  await expect(page.getByRole("button", { name: "Create organization" })).toHaveCount(0);
  await expect(page.getByLabel("Organization name")).toHaveCount(0);
  await expect(page.getByLabel("Slug")).toHaveCount(0);
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

test("org_limit_reached re-checks membership: a user who gained one meanwhile goes to the dashboard, not the dead-end", async ({ page }) => {
  // #2: between the funnel routing the user to create-org (0 orgs) and the create
  // refusal, they gained a membership (invite accepted elsewhere / JIT / admin add).
  // The 403 handler must re-check and send them to the dashboard — NOT the card.
  const ORG_OBJ = {
    id: ORG,
    name: "Joined Org",
    slug: "joined-org",
    pool_cidr: "10.99.0.0/24",
    created_at: new Date(0).toISOString(),
    updated_at: new Date(0).toISOString(),
  };
  let posted = false; // the membership "appears" only after the create is refused
  await page.route(ORGS_URL, (route) => {
    if (route.request().method() === "POST") {
      posted = true;
      return route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "org_limit_reached", message: "single organization only" } }),
      });
    }
    return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(posted ? [ORG_OBJ] : []) });
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
  await page.getByLabel("Organization name").fill("Whatever Org");
  await page.getByRole("button", { name: "Create organization" }).click();
  // Re-check found a membership → dashboard, and the invitation dead-end is NOT shown.
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Invitation required" })).toHaveCount(0);
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
  // Name the gateway: the token is PINNED to this name server-side, so the
  // ceremony must emit the COMPLETE env line incl. TUNNEX_NODE_NAME (Round-2
  // friction F1 — without it the agent loops node_name_mismatch).
  await page.getByLabel(/Gateway name/).fill("walk-gw");
  await page.getByRole("button", { name: "Generate join token" }).click();

  // The one-time ceremony: amber modal, token shown, must be acknowledged.
  await expect(page.getByText("Join token — shown once")).toBeVisible();
  // The COMPLETE env line for a name-pinned token: token AND the pinned name.
  await expect(page.locator("pre")).toHaveText(`TUNNEX_JOIN_TOKEN=${TOKEN} TUNNEX_NODE_NAME=walk-gw`);
  await expect(page.getByText(/pinned to the name/)).toBeVisible();
  await page.getByRole("button", { name: /I.?ve saved it/ }).click();
  // Dismissed → the token is gone from the page and was minted exactly once (it is
  // never re-served; re-enrolling would mint a NEW token).
  await expect(page.getByText(new RegExp(TOKEN))).toHaveCount(0);
  expect(issued).toBe(1);
});
