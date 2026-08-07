import { test, expect } from "@playwright/test";

// ⛔ SKIPPED, NOT DELETED — AND THE REASON IS THAT THE PRODUCT CHANGED UNDER THEM, NOT THAT THEY ARE WRONG.
//
// Public signup is closed (founder-ruled): a self-hosted control plane is owned by ONE COMPANY, install
// creates the CP admin, and everyone else arrives by INVITATION. These specs assert a self-serve signup
// flow that no longer exists — so they are red because the product moved, which is exactly the state a
// spec should end up in when a ruling lands.
//
// ⚠ THEY ARE NOT REWRITTEN HERE BECAUSE HALF THEIR REPLACEMENT IS NOT BUILT. The invitation flow — invite
// → email → the invited person SETS THEIR OWN PASSWORD from the link → signs in — is the next piece of the
// onboarding rebuild. Rewriting them against a model that is half-finished produces a suite that passes
// while testing nothing, which is worse than one that is honestly red.
//
// ⛔ TRIGGER, NAMED: the invitation flow. These are rewritten as invitation-shaped specs in that story, or
// deleted there with a reason. `docs/laws.md` — a deferred proof is deferred, never dropped.

// S4.2 auth screens. The demo owner (owner@demo.tunnex.local) is seeded, so it
// is a KNOWN-existing email for the enumeration-resistance check.
const EXISTING_EMAIL = "owner@demo.tunnex.local";

test.skip("signup renders identically for a new vs an existing email (no enumeration tell)", async ({
  page,
}) => {
  const confirm = page.getByRole("heading", { name: "Check your email" });

  // New (almost certainly unregistered) email.
  await page.goto("/signup");
  await page.getByLabel("Email").fill(`nobody-${Date.now()}@example.com`);
  await page.getByLabel("Password").fill("a-strong-passphrase-123");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(confirm).toBeVisible(); // wait out the transition
  const newBody = await page.locator("main p").first().textContent();

  // Existing email — must produce the SAME confirmation, not "already exists".
  await page.goto("/signup");
  await page.getByLabel("Email").fill(EXISTING_EMAIL);
  await page.getByLabel("Password").fill("a-strong-passphrase-123");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(confirm).toBeVisible();
  const existingBody = await page.locator("main p").first().textContent();

  // Identical heading (both reached "Check your email") AND identical body copy.
  expect(existingBody).toBe(newBody);
});

test("login shows a generic invalid-credentials message (no account enumeration)", async ({
  page,
}) => {
  await page.goto("/login");
  await page.getByLabel("Email").fill(EXISTING_EMAIL);
  await page.getByLabel("Password").fill("definitely-the-wrong-password");
  await page.getByRole("button", { name: "Sign in" }).click();
  // Server keeps this generic ("invalid email or password") — never "wrong password".
  await expect(page.getByText(/invalid email or password/i)).toBeVisible();
});

test("open edition hides SSO on the login page", async ({ page }) => {
  await page.goto("/login");
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  // The open build advertises no SSO providers, so no SSO section renders.
  await expect(page.getByText("Or sign in with SSO")).toHaveCount(0);
});

test.skip("login links to signup and password reset", async ({ page }) => {
  await page.goto("/login");
  await expect(
    page.getByRole("link", { name: "Create an account" }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Forgot password?" }),
  ).toBeVisible();
});

test("forgot-password renders identically for new vs existing email (no enumeration)", async ({
  page,
}) => {
  const confirm = page.getByRole("heading", { name: "Check your email" });
  await page.goto("/forgot-password");
  await page.getByLabel("Email").fill(`nobody-${Date.now()}@example.com`);
  await page.getByRole("button", { name: "Send reset link" }).click();
  await expect(confirm).toBeVisible();
  const newBody = await page.locator("main p").first().textContent();

  await page.goto("/forgot-password");
  await page.getByLabel("Email").fill(EXISTING_EMAIL);
  await page.getByRole("button", { name: "Send reset link" }).click();
  await expect(confirm).toBeVisible();
  expect(await page.locator("main p").first().textContent()).toBe(newBody);
});

test("verify-email with a bad token lands on a human-readable failure (not a raw error)", async ({
  page,
}) => {
  await page.goto("/verify-email?token=not-a-real-token");
  await expect(
    page.getByRole("heading", { name: "Verification failed" }),
  ).toBeVisible();
  // The token is scrubbed from the URL after capture (not left in history).
  await expect(page).toHaveURL(/\/verify-email$/);
});

test("SSO callback failures land on a human-readable login message (watch-item d)", async ({
  page,
}) => {
  await page.goto("/login?sso_error=unverified_local_exists");
  await expect(
    page.getByText(/account with this email already exists/i),
  ).toBeVisible();
});
