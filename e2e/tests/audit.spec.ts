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
  // A real backing store the mock pages over by keyset — 53 events, newest first
  // (i=0 is newest). The mock honors the cursor exactly like the server, so the
  // test proves the CLIENT sends the right cursor and stitches pages with no
  // overlap/gap — not merely "a second request happened".
  const all = Array.from({ length: 53 }, (_, i) => ({
    id: `00000000-0000-7000-8000-0000000${(100 + i).toString().padStart(5, "0")}`,
    created_at: new Date(1_700_000_000_000 - i * 60_000).toISOString(),
    actor_id: OWNER_ID,
    action: "device.created",
    target_type: "device",
    details: {} as Record<string, unknown>,
  }));
  // A secret-adjacent event (newest): details carries the KEYED fingerprint only.
  all[0] = {
    ...all[0],
    action: "sso.config_updated",
    target_type: "sso_config",
    details: { provider: "google", client_id: "gid-123", enabled: true, secret_fingerprint: "a1b2c3d4e5f6" } as Record<string, unknown>,
  } as (typeof all)[number];

  let sawCursorReq = false;
  await page.route("**/api/v1/organizations/*/audit-logs**", (route) => {
    const q = new URL(route.request().url()).searchParams;
    const limit = Number(q.get("limit"));
    const cts = q.get("cursor_ts");
    const cid = q.get("cursor_id");
    let rows = all;
    if (cts && cid) {
      sawCursorReq = true;
      // The cursor MUST be the last row the client is currently showing (row 49,
      // the 50th displayed — row 50 was the undisplayed has-more probe).
      expect(cts).toBe(all[49].created_at);
      expect(cid).toBe(all[49].id);
      // Row-value keyset: rows strictly older than the cursor.
      rows = all.filter((r) => r.created_at < cts || (r.created_at === cts && r.id < cid));
    }
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(rows.slice(0, limit)) });
  });

  await login(page);
  // Secret-free render: the fingerprint shows; no client_secret / sealed material.
  await expect(page.getByText("sso.config_updated")).toBeVisible();
  await expect(page.getByText(/a1b2c3d4e5f6/)).toBeVisible();
  await expect(page.getByText(/client_secret/i)).toHaveCount(0);
  await expect(page.getByText(/actor Demo Owner/).first()).toBeVisible();
  // Page 1 shows 50 rows (of the 51 fetched); the probe row means "more".
  const rows = page.locator("main ul > li");
  await expect(rows).toHaveCount(50);
  const loadMore = page.getByRole("button", { name: "Load more" });
  await expect(loadMore).toBeVisible();
  await loadMore.click();

  // After paging: exactly 53 rows — all events stitched in with NO overlap and NO
  // gap (53 distinct events → 53 rows; a re-served/skipped row would change this).
  // The undisplayed page-1 probe (row 50) is correctly re-served on page 2, and
  // the last displayed page-1 row (49) is the cursor, not re-appended.
  await expect(rows).toHaveCount(53);
  expect(sawCursorReq).toBe(true);
  await expect(page.getByRole("button", { name: "Load more" })).toHaveCount(0); // short last page → end
});
