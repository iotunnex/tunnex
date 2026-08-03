import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
  cleanup,
  within,
} from "@testing-library/react";

// SLICE 8 — AuditLog, and the last of the tier's accountable screens.
//
// IT NEARLY DID NOT EARN A TEST. The tier's definition is "the decision the user gets", and a read-only log
// that only displays would have been an honest EXEMPTION rather than a test asserting that data appears — the
// same judgement that exempted Dashboard. It earns one because it holds a real decision, stated in the
// product's own comment:
//
//   `filters` is the EDITING state; `applied` is the set that produced the current list — "Load more" must page
//   with `applied`, NEVER mid-edit `filters`, or the keyset cursor (from the applied list) mixes with a
//   different filter set.
//
// THE CONSEQUENCE: paging with mid-edit filters APPENDS A PAGE FROM A DIFFERENT QUERY to the current list.
// Not an error — WRONG DATA, silently, in the surface whose entire value is being trustworthy. An audit log
// that quietly interleaves two filter sets is worse than one that fails, because it is still legible.
//
// QUERY RULES 1-5 BIND.

afterEach(cleanup); // docs/laws.md — no globals/setup file, so auto-cleanup never registers

// Every audit-log request, captured at the NETWORK boundary — the query is the assertion target.
const queries: Array<Record<string, unknown>> = [];
let logFail = false;

// ⛔ `actor_id`, NOT `actor_user_id`. The mock sent a field the spec does not have — and ActivityEntry is
// `additionalProperties: false`, so the server can NEVER send it. The page reads `a.actor_id`, so every row
// in every audit-log test rendered the "system" FALLBACK and no test ever saw an actor name.
//
// MEASURED on the live API before changing this: 34 of 78 rows carry a populated `actor_id`, and its value
// is the acting user's uuid. THE PAGE WAS RIGHT; THE MOCK WAS WRONG.
//
//   A FALLBACK THAT IS NEVER EXERCISED DELIBERATELY IS A FALLBACK THAT IS ALWAYS EXERCISED ACCIDENTALLY.
const ENTRY = (id: string) => ({
  id,
  action: "device.created",
  created_at: `2026-08-01T10:00:0${id}Z`,
  actor_id: "u1",
  target_type: "device",
  target_id: "d1",
});

vi.mock("../src/lib/api", async () => {
  const actual =
    await vi.importActual<typeof import("../src/lib/api")>("../src/lib/api");
  return {
    ...actual,
    apiErrorMessage: (_e: unknown, f: string) => f,
    api: {
      GET: vi.fn(
        async (
          path: string,
          opts?: { params?: { query?: Record<string, unknown> } },
        ) => {
          if (path === "/api/v1/auth/me")
            return { data: { id: "u1", email: "a@b.c", email_verified: true } };
          if (path === "/api/v1/organizations")
            return { data: [{ id: "org-1", name: "Acme" }] };
          if (path.endsWith("/members"))
            return {
              data: [
                {
                  user_id: "u1",
                  email: "a@b.c",
                  name: "Ada Auditor",
                  role: "owner",
                  status: "active",
                  email_verified: true,
                  joined_at: "2026-01-01T00:00:00Z",
                },
              ],
            };
          if (path.endsWith("/audit-logs")) {
            queries.push(opts?.params?.query ?? {});
            if (logFail)
              return {
                data: undefined,
                error: { error: { code: "boom", message: "nope" } },
              };
            // PAGE+1 rows so the has-more probe trips and "Load more" is offered.
            return {
              data: Array.from({ length: 51 }, (_, i) => ENTRY(String(i))),
            };
          }
          return { data: [] };
        },
      ),
      POST: vi.fn(async () => ({ data: {} })),
    },
  };
});

import AuditLog from "../src/pages/AuditLog";
import { AuthProvider } from "../src/lib/auth";

const withAuth = (ui: React.ReactElement) =>
  render(<AuthProvider>{ui}</AuthProvider>);

beforeEach(() => {
  queries.length = 0;
  logFail = false;
});

describe("AuditLog — wiring: paging must use the APPLIED filter set, not the one being edited", () => {
  it("'Load more' pages with the filters that produced the list, ignoring an un-applied edit", async () => {
    withAuth(<AuditLog />);

    const loadMore = await waitFor(() =>
      screen.getByRole("button", { name: "Load more" }),
    );
    expect(queries.at(-1)?.action).toBeUndefined(); // the initial page: no filters applied

    // Edit a filter WITHOUT applying it. This is the mid-edit state the comment warns about.
    fireEvent.change(screen.getByPlaceholderText("e.g. device.created"), {
      target: { value: "policy.rule_enabled" },
    });

    fireEvent.click(loadMore);

    // THE DECISION: the next page must carry the APPLIED filter set (empty), plus a cursor. Carrying the
    // mid-edit `action` would append rows from a DIFFERENT query onto the current list — and the cursor,
    // which came from the applied list, would be meaningless against it.
    await waitFor(() => expect(queries.length).toBeGreaterThan(1));
    const paged = queries.at(-1)!;
    expect(paged.action).toBeUndefined();
    expect(paged.cursor_id).toBeDefined();
  });
});

describe("AuditLog — failure path", () => {
  // D1(b). An empty audit log reads as "nothing happened" — which on a compliance surface is a claim, not an
  // absence of data. A failed load has no standing to make it.
  it("a failed load is surfaced rather than rendering as an empty history", async () => {
    logFail = true;
    withAuth(<AuditLog />);

    await waitFor(() => screen.getByText("Could not load the audit log."));
  });
});

// ══════════════════════════════════════════════════════════════════════════════════════════════════════════
// THE ACTOR COLUMN — asserting a NAME, never the fallback.
//
// ⛔ WHY THIS TEST EXISTS AND DID NOT BEFORE. The mock sent `actor_user_id`; the page reads `actor_id`; the
// suite passed. Every row rendered "system", and no assertion ever looked at the actor column — so the only
// branch ever exercised was the one nobody wanted.
//
//   THE MOCK AND THE PAGE DISAGREED, THE TEST PASSED, AND THE PASSING BRANCH WAS THE ONE NOBODY WANTED.
//   A FALLBACK THAT IS NEVER EXERCISED DELIBERATELY IS A FALLBACK THAT IS ALWAYS EXERCISED ACCIDENTALLY.
//
// This is the audit surface, where mis-attributing a human act to the system is the exact failure the feature
// exists to prevent. So the assertion is on the NAME.
// ══════════════════════════════════════════════════════════════════════════════════════════════════════════

describe("AuditLog — the actor column names the human", () => {
  it("⛔ renders the ACTOR'S NAME for a human-actor row, not 'system'", async () => {
    withAuth(<AuditLog />);
    const table = await waitFor(() => screen.getByRole("table", { name: /audit|activity/i }));
    // The roster resolves u1 -> "Ada Auditor". If `actor_id` is ever renamed or dropped from the mock again,
    // this goes red instead of silently falling back.
    await waitFor(() => expect(within(table).getAllByText("Ada Auditor").length).toBeGreaterThan(0));

    // AND the fallback must NOT be what these rows render. Both halves: a name present, the fallback absent.
    const firstRow = within(table)
      .getAllByRole("row")
      .find((r) => r.textContent?.includes("Ada Auditor"))!;
    expect(firstRow.textContent).not.toMatch(/\bsystem\b/);
  });
});
