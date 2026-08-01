import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, cleanup } from "@testing-library/react";

// SLICE 6 — Settings. Second SHEDDER, and the consequence here is different in kind from every screen before it.
//
// ⚠ THE REDESIGN SPLITS THIS SCREEN. `Settings.tsx` keeps `settings` and sheds MACHINE CREDENTIALS to a new
// `cli` screen and EDITION to a new `license` screen. So the assertions below are written against the DECISION
// and NAME THE DESTINATION:
//
//   "the OpenVPN control reflects the org's ACTUAL opt-in state"   -> stays in `settings`
//   "an enterprise-only panel is hidden in the open edition"       -> travels to `license`
//   "the Settings page shows an OpenVPN card"                      -> does NOT travel; throwaway work
//
// THE CONSEQUENCE, and it is the decision under test: this screen renders ORG-LEVEL ENFORCEMENT CONFIG — MFA
// enforcement, SSO, device approval, OpenVPN opt-in. A wrong render here does not MISINFORM, it MISCONFIGURES:
// an admin toggles what they were SHOWN, not what is TRUE. Every other screen's worst case is a bad belief;
// this screen's worst case is a bad write.
//
// QUERY RULES 1-4 BIND: role + accessible name; NETWORK-boundary mocks; decisions not rendering; no viewport
// assumptions.

afterEach(cleanup); // docs/laws.md — no globals/setup file, so auto-cleanup never registers

let edition: "open" | "enterprise" = "enterprise";
let ovpnEnabled = false;

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
        if (path === "/api/v1/meta") return { data: { edition } };
        if (path === "/api/v1/organizations") {
          return {
            data: [
              {
                id: "org-1",
                name: "Acme",
                ovpn_enabled: ovpnEnabled,
                mfa_required: false,
              },
            ],
          };
        }
        if (path.endsWith("/members"))
          return {
            data: [{ user_id: "u1", role: "owner", email_verified: true }],
          };
        if (path.includes("/sso/"))
          return {
            data: undefined,
            error: { error: { code: "sso_not_configured" } },
          };
        return { data: [] };
      }),
      PUT: vi.fn(async () => ({ data: { enabled: true } })),
      POST: vi.fn(async () => ({ data: {} })),
      DELETE: vi.fn(async () => ({ data: {} })),
    },
  };
});

import Settings from "../src/pages/Settings";
import { AuthProvider } from "../src/lib/auth";

// The REAL AuthProvider — stubbing puts the TEST's role gate under assertion, not the PRODUCT's.
const withAuth = (ui: React.ReactElement) =>
  render(<AuthProvider>{ui}</AuthProvider>);

beforeEach(() => {
  edition = "enterprise";
  ovpnEnabled = false;
});

describe("Settings — wiring: the control must reflect the ORG'S state, not a default (destination: `settings`)", () => {
  // THE MISCONFIGURE DECISION. The button's LABEL is the affordance's meaning: "Enable OpenVPN" on an org that
  // already has it enabled invites an admin to turn ON what is already on — and the click writes the opposite
  // of what they intended. The label is not decoration; it IS the decision.
  it("an org with OpenVPN OFF offers ENABLE", async () => {
    ovpnEnabled = false;
    withAuth(<Settings />);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Enable OpenVPN" }),
      ).toBeTruthy(),
    );
    expect(
      screen.queryByRole("button", { name: "Disable OpenVPN" }),
    ).toBeNull();
  });

  it("an org with OpenVPN ON offers DISABLE — the inverse, so a default cannot satisfy both", async () => {
    ovpnEnabled = true;
    withAuth(<Settings />);
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "Disable OpenVPN" }),
      ).toBeTruthy(),
    );
    expect(screen.queryByRole("button", { name: "Enable OpenVPN" })).toBeNull();
  });
});

describe("Settings — wiring: edition gating (destination: `license`)", () => {
  // Decide-item 6 rules that ALL edition gating must route through ONE seam so S12.1 rewrites a hook and
  // nothing else. This asserts the DECISION — an enterprise-only surface is absent in the open edition —
  // which is the property that must survive both the split to `license` and the S12.1 refactor.
  it("SSO configuration is absent in the OPEN edition", async () => {
    edition = "open";
    withAuth(<Settings />);
    await waitFor(() => expect(screen.getByText(/Organization/i)).toBeTruthy());
    expect(screen.queryByText(/Microsoft Entra/i)).toBeNull();
    expect(screen.queryByText(/Google Workspace/i)).toBeNull();
  });

  it("SSO configuration is present in ENTERPRISE — the gate must not be a blanket hide", async () => {
    edition = "enterprise";
    withAuth(<Settings />);
    // The negative half. Without it, "hide everything always" satisfies the test above.
    await waitFor(() =>
      expect(screen.queryAllByText(/Entra|Google/i).length).toBeGreaterThan(0),
    );
  });
});

describe("Settings — failure path", () => {
  // D1(b), and it has teeth here for the reason stated at the top: a failed load that renders as "off" shows
  // every enforcement control disabled on an org that has them ENABLED. The org load is the one that gates the
  // whole page, so its failure must be SAID, never absorbed into a page of default-looking toggles.
  it("a failed organization load is surfaced, not rendered as a page of defaults", async () => {
    const api = (await import("../src/lib/api")).api as unknown as {
      GET: ReturnType<typeof vi.fn>;
    };
    api.GET.mockImplementation(async (path: string) => {
      if (path === "/api/v1/auth/me")
        return { data: { id: "u1", email: "a@b.c", email_verified: true } };
      if (path === "/api/v1/meta") return { data: { edition: "enterprise" } };
      if (path === "/api/v1/organizations")
        return { data: undefined, error: { error: { code: "boom" } } };
      return { data: [] };
    });

    withAuth(<Settings />);
    await waitFor(() =>
      expect(
        screen.getByText("Could not load your organizations."),
      ).toBeTruthy(),
    );
    // And no enforcement control may be offered against an org that was never loaded.
    expect(screen.queryByRole("button", { name: /OpenVPN/ })).toBeNull();
  });
});
