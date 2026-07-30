import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";

// THE FIRST COMPONENT TEST IN THIS REPO, and it exists for a gap the pure tier cannot close.
//
// Slice 3 extracted `defaultDeviceNode` into src/lib, which made the RULE testable. But a pure test of the rule
// passes just as happily while the page still reads `nodes[0]` — nothing asserted that the component uses the fix.
// That is the vacuous-check trap one tier up (docs/laws.md, COULD THIS CHECK HAVE FAILED?): the guard tests the
// extracted decision, not the decision the user actually gets.
//
// So this asserts the WIRING: given a fleet whose oldest gateway is revoked — the EPIC 11 walk's exact fleet
// state, where `aws-gw-1` was revoked and had the earliest created_at and therefore WAS nodes[0] — the POST that
// creates a device must carry the ACTIVE gateway's id.
//
// Scope is deliberately Slice 3's surfaces. This is the foothold for the registered component-test-tier ledger
// item, not a retroactive suite for the whole app.

const posts: Array<{ path: string; body: Record<string, unknown> }> = [];

vi.mock("../src/lib/api", () => ({
  apiErrorMessage: (_e: unknown, fallback: string) => fallback,
  api: {
    GET: vi.fn(async (path: string) => {
      if (path === "/api/v1/organizations") return { data: [{ id: "org-1", name: "Acme" }] };
      if (path.endsWith("/nodes")) {
        return {
          data: [
            // REVOKED and FIRST — ListNodes orders by created_at, so this is what `nodes[0]` used to select.
            { id: "revoked-oldest", name: "aws-gw-1", status: "revoked", agent_version: "0.1.0" },
            { id: "live-gateway", name: "aws-gw-2", status: "active", agent_version: "0.1.0" },
          ],
        };
      }
      return { data: [] };
    }),
    POST: vi.fn(async (path: string, opts: { body: Record<string, unknown> }) => {
      posts.push({ path, body: opts.body });
      return { data: { device: { status: "active" }, config: "wg-conf" } };
    }),
  },
}));

// qrcode.react pulls a canvas-ish dependency chain that jsdom does not need for this assertion.
vi.mock("qrcode.react", () => ({ QRCodeSVG: () => null }));

import Devices from "../src/pages/Devices";

describe("device creation homes on an ACTIVE gateway (S13.1 Slice 3 — the wiring)", () => {
  beforeEach(() => {
    posts.length = 0;
  });

  it("posts the active gateway's id even when a revoked gateway sorts first", async () => {
    render(<Devices />);

    // Wait for the fleet to load, then create a device through the real form.
    const nameInput = await waitFor(() => screen.getByPlaceholderText("my-laptop"));
    fireEvent.change(nameInput, { target: { value: "test-laptop" } });
    fireEvent.click(screen.getByRole("button", { name: /create device/i }));

    await waitFor(() => expect(posts.length).toBeGreaterThan(0));
    const created = posts.find((p) => p.path.endsWith("/devices"));
    expect(created, "a device POST must have been issued").toBeTruthy();
    expect(
      created!.body.node_id,
      "the device must be homed on the ACTIVE gateway; homing it on the revoked one issues a one-time config " +
        "that can never connect, and a one-time secret cannot be re-issued",
    ).toBe("live-gateway");
  });
});
