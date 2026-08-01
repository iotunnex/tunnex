import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, cleanup } from "@testing-library/react";

// SLICE 2 — Devices. It is the REFERENCE IMPLEMENTATION for revoked-suppression: the surface that always had
// the guard (`d.status !== "revoked" && …`) while Gateways.tsx lacked it (WF-S11-10) and Sites.tsx still lacked
// it until this branch. So its wiring test is also HALF OF D4's three-way assertion, and the sibling file reads
// from the same production functions rather than restating the rule — a fixture that restates production tests
// the restatement, which is WF-S13-3's class.
//
// QUERY STRATEGY (docs/UI-REDESIGN-registration.md consequence 2): role + accessible name for anything that
// carries a role; mocking at the NETWORK boundary (api.GET / api.POST), never the component boundary, because
// that layer does not change in a redesign. getByText appears only for content with NO role today — status
// badges and empty states are <span>/<li> — and each such use is a MARKER that the element should gain
// role="status"/role="alert" in the redesign, not an exemption.

afterEach(cleanup); // docs/laws.md — no globals/setup file, so auto-cleanup never registers

let devicesFail = false;
const DEVICES = [
  // REVOKED and posture-bearing: the row whose badges must be suppressed. This is the shape Gateways got wrong.
  {
    id: "d-revoked",
    name: "old-laptop",
    status: "revoked",
    assigned_ip: "10.99.0.9",
    health_state: "noncompliant",
    health_blocked: true,
    needs_reexport: true,
  },
  {
    id: "d-active",
    name: "work-laptop",
    status: "active",
    assigned_ip: "10.99.0.3",
    health_state: "noncompliant",
    health_blocked: true,
    needs_reexport: true,
  },
];

vi.mock("../src/lib/api", async () => {
  const actual = await vi.importActual<typeof import("../src/lib/api")>("../src/lib/api");
  return {
    ...actual,
    apiErrorMessage: (_e: unknown, fallback: string) => fallback,
    api: {
      GET: vi.fn(async (path: string) => {
        if (path === "/api/v1/organizations") return { data: [{ id: "org-1", name: "Acme" }] };
        if (path.endsWith("/devices")) {
          // THE FAILURE PATH under test: the load REFUSES. The page must not render this as "no devices".
          if (devicesFail) return { data: undefined, error: { error: { code: "boom", message: "nope" } } };
          return { data: DEVICES };
        }
        if (path.endsWith("/nodes")) return { data: [{ id: "n-1", name: "gw", status: "active", agent_version: "0.1.0" }] };
        return { data: [] };
      }),
      POST: vi.fn(async () => ({ data: {} })),
    },
  };
});

import Devices from "../src/pages/Devices";

beforeEach(() => {
  devicesFail = false;
});

describe("Devices — wiring", () => {
  it("a REVOKED device carries no posture badge and no re-export instruction; an active one carries both", async () => {
    render(<Devices />);
    await waitFor(() => expect(screen.getByText("old-laptop")).toBeTruthy());

    // Two devices are posture-blocked and both need re-export. Only the ACTIVE one may say so — the revoked
    // row's badges would describe a device that is no longer meant to work, and "re-export needed" would be an
    // instruction to act on a device that cannot come back.
    expect(screen.getAllByText("posture blocked")).toHaveLength(1);
    expect(screen.getAllByText("re-export needed")).toHaveLength(1);
  });

  it("both devices are listed — suppression hides BADGES, never the row itself", async () => {
    render(<Devices />);
    await waitFor(() => expect(screen.getByText("old-laptop")).toBeTruthy());
    // The distinction matters: an operator must still see a revoked device exists. Suppressing the row would
    // trade a wrong badge for a missing fact.
    expect(screen.getByText("work-laptop")).toBeTruthy();
    expect(screen.getByText("10.99.0.9")).toBeTruthy();
  });
});

describe("Devices — failure path", () => {
  // D1(b). The loadOne law's violation mode is a REASSURING EMPTY STATE: the screen renders perfectly and tells
  // the user nothing. `loadDevices` sets the error and returns EARLY, so `devices` stays empty and the page
  // shows both the error and "No devices yet." — the error is what must never go missing.
  it("a failed device load is SURFACED, never swallowed into 'no devices'", async () => {
    devicesFail = true;
    render(<Devices />);

    await waitFor(() => expect(screen.getByText("Could not load devices.")).toBeTruthy());
  });

  it("an empty-but-successful load says so in words", async () => {
    devicesFail = false;
    DEVICES.length = 0; // an org with no devices — a FACT, not a failure
    render(<Devices />);
    await waitFor(() => expect(screen.getByText("No devices yet.")).toBeTruthy());
    // And with no failure, no error line is present — the two states must stay distinguishable.
    expect(screen.queryByText("Could not load devices.")).toBeNull();
    DEVICES.push(
      { id: "d-revoked", name: "old-laptop", status: "revoked", assigned_ip: "10.99.0.9", health_state: "noncompliant", health_blocked: true, needs_reexport: true },
      { id: "d-active", name: "work-laptop", status: "active", assigned_ip: "10.99.0.3", health_state: "noncompliant", health_blocked: true, needs_reexport: true },
    );
  });
});
