import { test, expect, type Page } from "@playwright/test";

// S4.4 Users & roles UI. Runs SERIALLY and leaves the seed state as it found it
// (the role-change test reverts) so it can't interfere with itself or other
// specs. The seed provides an owner and a plain member in the demo org.
test.describe.configure({ mode: "serial" });

const OWNER = { email: "owner@demo.tunnex.local", pass: "tunnex-demo-password" };
const MEMBER = { email: "member@demo.tunnex.local", pass: "tunnex-demo-password" };

async function loginAs(page: Page, who: { email: string; pass: string }) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(who.email);
  await page.getByLabel("Password").fill(who.pass);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await page.getByRole("link", { name: "Users" }).click();
  await expect(page.getByRole("heading", { name: "Users" })).toBeVisible();
}

// (a) DENY view: a plain member may see the roster but is offered NO management
// controls — the UI mirrors the RBAC matrix (member lacks member:invite/manage).
test("a member sees the roster but no management controls", async ({ page }) => {
  await loginAs(page, MEMBER);
  // Scope to roster rows (<li>) — the logged-in user's email also shows in the
  // header, so a bare getByText would match twice.
  await expect(page.locator("li", { hasText: OWNER.email })).toBeVisible();
  await expect(page.locator("li", { hasText: MEMBER.email })).toBeVisible();
  // No invite form, and no role <select> or Deactivate anywhere.
  await expect(page.getByLabel("Invite by email")).toHaveCount(0);
  await expect(page.locator("select")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Deactivate" })).toHaveCount(0);
});

// (a) ALLOW view: an owner gets the invite form and per-member controls.
test("an owner sees the invite form and per-member controls", async ({ page }) => {
  await loginAs(page, OWNER);
  await expect(page.getByLabel("Invite by email")).toBeVisible();
  const memberRow = page.locator("li", { hasText: MEMBER.email });
  await expect(memberRow.locator("select")).toBeVisible();
  await expect(memberRow.getByRole("button", { name: "Deactivate" })).toBeVisible();
});

// (b) Last-owner: the sole owner's own role control is disabled-with-explanation
// (client mirror of the server's last-owner refusal).
test("the sole owner's own role control is disabled with an explanation", async ({ page }) => {
  await loginAs(page, OWNER);
  const ownerRow = page.locator("li", { hasText: OWNER.email });
  const roleSelect = ownerRow.locator("select");
  await expect(roleSelect).toBeDisabled();
  await expect(roleSelect).toHaveAttribute("title", /at least one owner/i);
});

// (c) Invite enumeration resistance: inviting an address that already has an
// account renders IDENTICALLY to inviting a brand-new one — no account-existence
// tell.
test("invite renders identically for an existing account vs a new email", async ({ page }) => {
  await loginAs(page, OWNER);
  const ORG = "01900000-0000-7000-8000-000000000001"; // seeddata.DemoOrgID

  async function invite(email: string): Promise<string> {
    await page.getByLabel("Invite by email").fill(email);
    await page.getByRole("button", { name: "Send invite" }).click();
    const msg = page.getByText(/invitation is on its way/i);
    await expect(msg).toBeVisible(); // if the invite errored, sent stays false and this times out
    return (await msg.textContent()) ?? "";
  }

  const fresh = `nobody-${Date.now()}@example.com`;
  const existingMsg = await invite(MEMBER.email); // an address that already has an account
  const newMsg = await invite(fresh); // a brand-new address
  // Identical confirmation for both → the response can't be used to tell whether
  // the address already has an account.
  expect(existingMsg).toBe(newMsg);

  // Clean up the pending invites we created so the test is re-runnable (the
  // invite table would otherwise 409 "invite_pending" on the next run). Uses the
  // owner's authenticated session shared with the page context.
  for (const email of [MEMBER.email, fresh]) {
    await page.request.post(`/api/v1/organizations/${ORG}/invitations/revoke`, {
      headers: { "X-Tunnex-CSRF": "1" },
      data: { email },
    });
  }
});

// (e) Audit loop: a mutation in the Users UI lands in the audit log and shows up
// on the dashboard activity feed (the cheapest full-stack proof: UI -> API ->
// audit -> dashboard read -> render). Reverts the role to leave state clean.
test("a role change in the UI appears on the dashboard activity feed", async ({ page }) => {
  await loginAs(page, OWNER);
  const memberRow = page.locator("li", { hasText: MEMBER.email });
  await memberRow.locator("select").selectOption("admin");

  await page.getByRole("link", { name: "Dashboard" }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(page.getByText("member.role_changed").first()).toBeVisible();

  // Revert so the roster is left as seeded (member stays a 'member').
  await page.getByRole("link", { name: "Users" }).click();
  await page.locator("li", { hasText: MEMBER.email }).locator("select").selectOption("member");
});
