import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { MemoryRouter } from "react-router-dom";
import {
  render,
  screen,
  waitFor,
  cleanup,
  within,
} from "@testing-library/react";

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
  {
    id: "d-pending",
    name: "unapproved-phone",
    status: "pending",
    assigned_ip: "10.99.0.15",
  },
];

vi.mock("../src/lib/api", async () => {
  const actual =
    await vi.importActual<typeof import("../src/lib/api")>("../src/lib/api");
  return {
    ...actual,
    apiErrorMessage: (_e: unknown, fallback: string) => fallback,
    api: {
      GET: vi.fn(async (path: string) => {
        if (path === "/api/v1/organizations")
          return { data: [{ id: "org-1", name: "Acme" }] };
        if (path.endsWith("/devices")) {
          // THE FAILURE PATH under test: the load REFUSES. The page must not render this as "no devices".
          if (devicesFail)
            return {
              data: undefined,
              error: { error: { code: "boom", message: "nope" } },
            };
          return { data: DEVICES };
        }
        if (path.endsWith("/nodes"))
          return {
            data: [
              {
                id: "n-1",
                name: "gw",
                status: "active",
                agent_version: "0.1.0",
              },
            ],
          };
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

// ⚠ RE-POINTED AT ROLES IN S14.3 SLICE A, and the re-pointing is half the slice.
//
// These assertions used to match device names as FREE TEXT, because until slice A there was no `<table>`
// anywhere in the app and therefore no `role="row"` or `role="cell"` to ask for. The primitive's absence had
// made the TESTS weaker, not only the UI — and a primitive that ships while its consumers keep the workaround
// has only half landed (docs/laws.md).
//
// What changes materially: `getByText("old-laptop")` passes if that string appears ANYWHERE — a heading, a
// tooltip, a modal, a toast. `within(row).getByText(...)` passes only if it is in THAT DEVICE'S ROW. The old
// query could not tell "the revoked device shows a posture badge" from "a posture badge exists on the page".

/** The row for a device, found by its name — the query that was impossible before slice A. */
function rowFor(name: string): HTMLElement {
  const table = screen.getByRole("table", { name: "Devices" });
  const row = within(table)
    .getAllByRole("row")
    .find((r) => within(r).queryByText(name));
  if (!row) throw new Error(`no row for device "${name}"`);
  return row;
}

describe("Devices — wiring", () => {
  it("a REVOKED device carries no posture badge and no re-export instruction; an active one carries both", async () => {
    render(
      <MemoryRouter>
        <Devices />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(screen.getByRole("table", { name: "Devices" })).toBeTruthy(),
    );

    // Two devices are posture-blocked and both need re-export. Only the ACTIVE one may say so — the revoked
    // row's badges would describe a device that is no longer meant to work, and "re-export needed" would be an
    // instruction to act on a device that cannot come back.
    //
    // ASSERTED PER ROW, which is the upgrade. The old page-wide count would have passed even if both badges
    // sat on the WRONG device, as long as there was one of each.
    expect(
      within(rowFor("work-laptop")).queryByText("posture blocked"),
    ).toBeTruthy();
    expect(
      within(rowFor("work-laptop")).queryByText("re-export needed"),
    ).toBeTruthy();
    expect(
      within(rowFor("old-laptop")).queryByText("posture blocked"),
    ).toBeNull();
    expect(
      within(rowFor("old-laptop")).queryByText("re-export needed"),
    ).toBeNull();
  });

  it("both devices are listed — suppression hides BADGES, never the row itself", async () => {
    render(
      <MemoryRouter>
        <Devices />
      </MemoryRouter>,
    );
    const table = await waitFor(() =>
      screen.getByRole("table", { name: "Devices" }),
    );
    // The distinction matters: an operator must still see a revoked device exists. Suppressing the row would
    // trade a wrong badge for a missing fact.
    //
    // 4 rows = 1 header + 3 devices (revoked, active, pending). Counting rows is a stronger claim than "these strings appear": it
    // also fails if an unexpected device were rendered, which text matching could never notice.
    expect(within(table).getAllByRole("row")).toHaveLength(4);
    // The address and pending status are asserted ON THEIR OWN DEVICE'S ROW.
    expect(within(rowFor("old-laptop")).getByText("10.99.0.9")).toBeTruthy();
    expect(within(rowFor("unapproved-phone")).getByText("pending")).toBeTruthy();
  });

  it("the table names its columns — a cell with no header is a value nobody can identify", async () => {
    render(
      <MemoryRouter>
        <Devices />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(screen.getByRole("table", { name: "Devices" })).toBeTruthy(),
    );
    for (const h of ["Device", "Address", "State", "Posture", "Actions"]) {
      expect(screen.getByRole("columnheader", { name: h }), h).toBeTruthy();
    }
  });
});

describe("Devices — failure path", () => {
  // D1(b). The loadOne law's violation mode is a REASSURING EMPTY STATE: the screen renders perfectly and tells
  // the user nothing. `loadDevices` sets the error and returns EARLY, so `devices` stays empty and the page
  // shows both the error and "No devices yet." — the error is what must never go missing.
  it("a failed device load is SURFACED, never swallowed into 'no devices'", async () => {
    devicesFail = true;
    render(
      <MemoryRouter>
        <Devices />
      </MemoryRouter>,
    );

    await waitFor(() =>
      expect(screen.getByText("Could not load devices.")).toBeTruthy(),
    );
  });

  it("an empty-but-successful load says so in words", async () => {
    devicesFail = false;
    DEVICES.length = 0; // an org with no devices — a FACT, not a failure
    render(
      <MemoryRouter>
        <Devices />
      </MemoryRouter>,
    );
    await waitFor(() =>
      expect(screen.getByText("No devices yet.")).toBeTruthy(),
    );
    // And with no failure, no error line is present — the two states must stay distinguishable.
    expect(screen.queryByText("Could not load devices.")).toBeNull();
    DEVICES.push(
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
    );
  });
});
