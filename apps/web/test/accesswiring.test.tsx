import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, cleanup } from "@testing-library/react";

// SLICE 4 — Access. Last of the four ranked screens, and the only one whose case is CONSEQUENCE-based rather
// than finding-based. So the consequence is stated here, because it is the decision under test:
//
//   A RULE SHOWN AS ACTIVE BUT NOT COMPILED IS A SILENT AUTHORIZATION GAP —
//   the UI asserting access the gateway does not enforce.
//
// That is not the rule list's RENDERING. It is the relationship between what this screen claims about
// enforcement and what is actually being enforced, and it fails in two directions:
//
//   (a) rules listed while the org's mode is OFF        -> the UI implies enforcement that does not exist
//   (b) a FAILED load rendered as a count or an empty    -> the UI asserts a posture it never read
//
// Both are encoded in `rulesSummary`, which is why the assertions below drive it through the real page rather
// than restating its branches.
//
// QUERY RULES 1-4 BIND (docs/UI-REDESIGN-registration.md consequence 2): role + accessible name; mocked at the
// NETWORK boundary; decisions not rendering; and NO ASSERTION MAY ASSUME A VIEWPORT — nothing below depends on
// layout, column order, or an element visible only at one width.

afterEach(cleanup); // docs/laws.md — no globals/setup file, so auto-cleanup never registers

let mode: "off" | "enforcing" = "enforcing";
let rulesFail = false;

const RULES = [
  {
    id: "r-enabled",
    enabled: true,
    src_kind: "group",
    dst_kind: "resource",
    src_group_id: "g1",
    dst_resource_id: "res1",
  },
  {
    id: "r-disabled",
    enabled: false,
    src_kind: "group",
    dst_kind: "resource",
    src_group_id: "g1",
    dst_resource_id: "res1",
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
          return { data: { id: "u1", email: "a@b.c", email_verified: true } };
        if (path === "/api/v1/meta") return { data: { edition: "enterprise" } };
        if (path === "/api/v1/organizations")
          return { data: [{ id: "org-1", name: "Acme" }] };
        if (path.endsWith("/members"))
          return {
            data: [{ user_id: "u1", role: "admin", email_verified: true }],
          };
        if (path.endsWith("/zero-trust-mode")) return { data: { mode } };
        if (path.endsWith("/policies")) {
          if (rulesFail)
            return {
              data: undefined,
              error: { error: { code: "boom", message: "nope" } },
            };
          return { data: RULES };
        }
        return { data: [] };
      }),
      POST: vi.fn(async () => ({ data: {} })),
      PATCH: vi.fn(async () => ({ data: {} })),
      DELETE: vi.fn(async () => ({ data: {} })),
    },
  };
});

import Access from "../src/pages/Access";
import { AuthProvider } from "../src/lib/auth";

// The REAL AuthProvider. Stubbing the context would put the TEST's copy of the role gate under assertion
// instead of the PRODUCT's — fixture-restates-production at the seam that most invites it (docs/laws.md).
const withAuth = (ui: React.ReactElement) =>
  render(<AuthProvider>{ui}</AuthProvider>);

beforeEach(() => {
  mode = "enforcing";
  rulesFail = false;
});

describe("Access — wiring: the screen must not claim enforcement it does not have", () => {
  it("with mode OFF, the posture says NOT ENFORCED — rules present must not imply they are in force", async () => {
    mode = "off";
    withAuth(<Access />);

    // The gap in direction (a): two rules exist and are listed, but nothing is enforcing them. The screen has
    // to say so, or an admin reads a rule list as an access-control posture that the gateway is not applying.
    await waitFor(() =>
      expect(screen.getByText("Policy not enforced. Open mesh: every device reaches every device.")).toBeTruthy(),
    );
    expect(screen.queryByText(/Default-deny active/)).toBeNull();
  });

  it("with mode ENFORCING, the posture names default-deny", async () => {
    mode = "enforcing";
    withAuth(<Access />);
    await waitFor(() =>
      expect(screen.getByText(/Default-deny active/)).toBeTruthy(),
    );
  });

  it("a DISABLED rule is shown distinctly, never hidden — the list must not lie about what is enforcing", async () => {
    withAuth(<Access />);
    // F3's rule. Hiding a disabled rule would make the list read as the complete set of what is in force,
    // which is the same lie as (a) one row down.
    await waitFor(() => expect(screen.getByText("disabled")).toBeTruthy());
  });
});

describe("Access — failure path: the most consequential one in the product", () => {
  // D1(b), and it matters most here. The loadOne law's violation mode is a REASSURING EMPTY STATE — and on this
  // surface "no rules" is not a neutral emptiness. Under default-deny it reads as "nothing is permitted"; under
  // mode-off it reads as "nothing is restricted". Either way the screen would be asserting an authorization
  // posture it never successfully read.
  it("a failed rules load renders a retry, NEVER 'no rules'", async () => {
    rulesFail = true;
    withAuth(<Access />);

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Retry" })).toBeTruthy(),
    );
  });

  it("a failed load never renders a defaulted rule COUNT — it says the status is unavailable", async () => {
    rulesFail = true;
    withAuth(<Access />);

    await waitFor(() =>
      expect(
        screen.getByText("Rule status unavailable. Refresh to try again."),
      ).toBeTruthy(),
    );
    // The specific lie this prevents: "0 rules — ALL traffic denied." on a load that never returned. A count
    // derived from a failure is an authorization claim invented by the client.
    expect(screen.queryByText(/ALL traffic denied/)).toBeNull();
  });
});
