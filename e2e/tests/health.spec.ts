import { test, expect } from "@playwright/test";

test("SPA loads and reports the API operational", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByText("tunnex", { exact: false }).first()).toBeVisible();
  // The health pill flips to "operational" once the SPA's /healthz call succeeds.
  await expect(page.getByText("operational")).toBeVisible();
});

test("correlation chain: SPA -> API -> X-Request-Id is well-formed and matches body", async ({
  page,
}) => {
  // Capture the /healthz response the SPA triggers on load.
  const healthResponse = page.waitForResponse(
    (r) => r.url().endsWith("/healthz") && r.request().method() === "GET",
  );
  await page.goto("/");
  const resp = await healthResponse;

  expect(resp.ok()).toBeTruthy();

  const requestId = resp.headers()["x-request-id"];
  expect(requestId, "X-Request-Id header must be present").toBeTruthy();
  // nginx stamps a 32-char hex correlation id and the API echoes it.
  expect(requestId).toMatch(/^[a-f0-9]{32}$/);

  const body = (await resp.json()) as { status: string; request_id?: string };
  expect(body.status).toBe("ok");
  expect(body.request_id).toBe(requestId);
});
