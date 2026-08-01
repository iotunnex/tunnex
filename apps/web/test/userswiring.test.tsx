import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  cleanup,
  within,
} from "@testing-library/react";

// SLICE 7 — Users. Ranked here on the CONSEQUENCE criterion, and the founder's correction stands: this screen
// is not read-only in either sense. It renders roles and it CHANGES what people can do.
//
// THE DECISION UNDER TEST is the sole-owner guard. `isSoleOwner(m) = m.role === "owner" && ownerCount <= 1`
// disables the role control on the last owner, because an organization must always have at least one owner.
// Getting it wrong is not a bad belief — it is a LOCKOUT: demote the last owner and nobody can administer the
// org again, including the person who did it.
//
// `ownerCount` is DERIVED FROM THE LOADED ROSTER, which is what makes it a wiring decision rather than a pure
// one: the guard is only as correct as the list it counts. That is the same shape as WF-S11-10b, where a count
// walked a query that did not filter what the operator assumed it filtered.
//
// QUERY RULES 1-5 BIND: role + accessible name; NETWORK-boundary mocks; decisions not rendering; no viewport
// assumptions; and every waitFor covers EVERY element the assertions touch.

afterEach(cleanup); // docs/laws.md — no globals/setup file, so auto-cleanup never registers

let membersFail = false;
let roster = [
  {
    user_id: "u1",
    email: "owner@acme.test",
    role: "owner",
    email_verified: true,
    active: true,
  },
  {
    user_id: "u2",
    email: "admin@acme.test",
    role: "admin",
    email_verified: true,
    active: true,
  },
];

vi.mock("../src/lib/api", async () => {
  const actual =
    await vi.importActual<typeof import("../src/lib/api")>("../src/lib/api");
  return {
    ...actual,
    apiErrorMessage: (_e: unknown, f: string) => f,
    api: {
      GET: vi.fn(async (path: string) => {
        if (path === "/api/v1/auth/me")
          return {
            data: { id: "u1", email: "owner@acme.test", email_verified: true },
          };
        if (path === "/api/v1/meta") return { data: { edition: "enterprise" } };
        if (path === "/api/v1/organizations")
          return { data: [{ id: "org-1", name: "Acme" }] };
        if (path.endsWith("/members")) {
          if (membersFail)
            return {
              data: undefined,
              error: { error: { code: "boom", message: "nope" } },
            };
          return { data: roster };
        }
        return { data: [] };
      }),
      POST: vi.fn(async () => ({ data: {} })),
      PUT: vi.fn(async () => ({ data: {} })),
    },
  };
});

import Users from "../src/pages/Users";
import { AuthProvider } from "../src/lib/auth";

// The REAL AuthProvider — stubbing puts the TEST's role gate under assertion, not the PRODUCT's.
const withAuth = (ui: React.ReactElement) =>
  render(<AuthProvider>{ui}</AuthProvider>);

beforeEach(() => {
  membersFail = false;
  roster = [
    {
      user_id: "u1",
      email: "owner@acme.test",
      role: "owner",
      email_verified: true,
      active: true,
    },
    {
      user_id: "u2",
      email: "admin@acme.test",
      role: "admin",
      email_verified: true,
      active: true,
    },
  ];
});

describe("Users — wiring: the last owner cannot be demoted", () => {
  it("the SOLE owner's role control is disabled, and says why", async () => {
    withAuth(<Users />);

    // Query rule 5: wait for THE THING ASSERTED. (Not an email — those appear more than once; and not a
    // combobox count — the invite form contributes a third.)
    const soleOwnerControl = await waitFor(() =>
      screen.getByTitle("An organization must always have at least one owner."),
    );
    expect((soleOwnerControl as HTMLSelectElement).disabled).toBe(true);
  });

  it("with TWO owners, neither control is disabled — 'always disabled' must not pass", async () => {
    // The negative half. Without it, disabling every role control satisfies the assertion above while making
    // the screen useless — and a lockout guard that never lets anyone change a role is its own outage.
    roster = [
      {
        user_id: "u1",
        email: "owner@acme.test",
        role: "owner",
        email_verified: true,
        active: true,
      },
      {
        user_id: "u2",
        email: "second@acme.test",
        role: "owner",
        email_verified: true,
        active: true,
      },
    ];
    withAuth(<Users />);
    // An ABSENCE assertion needs a POSITIVE anchor proving the roster rendered, or it is trivially true against
    // a tree that has not finished — the async form (docs/laws.md). Anchor on the second owner's row.
    // RE-POINTED IN S14.3 SLICE A. `getAllByText(email).length > 0` passed if the address appeared anywhere
    // and said nothing about WHOSE row the role control belonged to. Now the member is a row, and the role
    // control is asserted INSIDE it — which is the assertion the screen actually needs, since a role select
    // wired to the wrong member is the failure that matters here.
    const table = await waitFor(() =>
      screen.getByRole("table", { name: "Members" }),
    );
    const row = within(table)
      .getAllByRole("row")
      // queryAllByText, not queryByText: a member with no display name renders the email TWICE in its own
      // cell (as the name and as the address), and `queryBy*` throws on multiple matches. The row predicate
      // only needs "does this row mention them", so the count is irrelevant.
      .find((r) => within(r).queryAllByText("second@acme.test").length > 0)!;
    expect(row, "no row for second@acme.test").toBeTruthy();
    expect(
      within(row).getByRole("combobox", { name: "Role for second@acme.test" }),
    ).toBeTruthy();

    expect(
      screen.queryByTitle(
        "An organization must always have at least one owner.",
      ),
    ).toBeNull();
  });
});

describe("Users — failure path", () => {
  // D1(b). An empty roster on this screen does not read as "no data" — it reads as "this org has no members",
  // which for an org that HAS members is a claim about who can administer it. The guard here is explicit in
  // the product (`members.length === 0 && !error`), so the test asserts that the error wins.
  it("a failed roster load is surfaced and does NOT render as 'no members yet'", async () => {
    membersFail = true;
    withAuth(<Users />);

    await waitFor(() => screen.getByText("Could not load members."));
    expect(screen.queryByText("No members yet.")).toBeNull();
    // AND the table itself is absent, not merely empty. This is the assertion that would have caught the
    // defect this slice introduced and the tier found: converting the roster to a table dropped the page's
    // `&& !error` guard, so a failed load rendered "No members yet." — a claim about who can administer the
    // org, made by a screen that never read anything. `failed` is now a REQUIRED prop on DataTable, so
    // forgetting it is a compile error rather than a review note.
    expect(screen.queryByRole("table", { name: "Members" })).toBeNull();
  });
});
