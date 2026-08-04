import { describe, expect, it, afterEach, vi, beforeEach } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MachineCredentials } from "../src/components/MachineCredentials";
import * as apiMod from "../src/lib/api";

// ⛔ THE THREE EMPTY STATES ARE THE POINT OF THIS SCREEN, AND THE THIRD IS WHY IT IS HARD.
//
// none · all owned · THE LIST FAILED TO LOAD. An unreachable query rendering as an empty list is
// "migration complete" written by an error path — and this is a MIGRATION screen, so that is precisely the
// reassurance that must be earned rather than defaulted to.
//
// The component previously used a raw `api.GET`, which is review-refused on a list whose emptiness is
// user-meaningful: a non-2xx and a network REJECTION are different paths, and reading only `data` renders a
// reassuring "none" for both.

const ORG = "11111111-1111-1111-1111-111111111111";

function cred(over: Partial<Record<string, unknown>> = {}) {
  return {
    id: String(over.id ?? "c1"),
    name: String(over.name ?? "gitops"),
    fingerprint: "fp-abc",
    created_at: new Date(Date.now() - 86_400_000).toISOString(),
    last_used_at: over.last_used_at ?? null,
    owner_user_id: over.owner_user_id ?? null,
  };
}

/** GET returns per-path fixtures; anything unlisted rejects, which is itself the failure case. */
function stubGet(byPath: Record<string, unknown>) {
  vi.spyOn(apiMod.api, "GET").mockImplementation((async (path: string) => {
    if (path in byPath) {
      const v = byPath[path];
      if (v === "REJECT") return Promise.reject(new Error("network down"));
      return { data: v };
    }
    return { data: [] };
  }) as never);
}

beforeEach(() => vi.restoreAllMocks());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const LIST = "/api/v1/organizations/{orgId}/machine-credentials";
const MEMBERS = "/api/v1/organizations/{orgId}/members";

describe("⛔ the three empty states are distinguishable", () => {
  it("NONE — no credentials exist, and it says there is nothing to assign", async () => {
    stubGet({ [LIST]: [], [MEMBERS]: [] });
    render(<MachineCredentials orgId={ORG} canManage />);
    await waitFor(() =>
      expect(screen.getByText(/no machine credentials exist/i)).toBeTruthy(),
    );
    expect(document.querySelector('[data-state="load-failed"]')).toBeNull();
  });

  it("⛔ FAILED TO LOAD is NOT 'none' — the state this screen exists to keep apart", async () => {
    stubGet({ [LIST]: "REJECT", [MEMBERS]: [] });
    render(<MachineCredentials orgId={ORG} canManage />);
    await waitFor(() =>
      expect(document.querySelector('[data-state="load-failed"]')).not.toBeNull(),
    );
    // The failure must NOT be readable as absence, and must say so in words.
    expect(screen.queryByText(/no machine credentials exist/i)).toBeNull();
    expect(screen.getByText(/not the same as having none/i)).toBeTruthy();
  });

  it("ALL OWNED — earned, and rendered ABOVE the rows", async () => {
    stubGet({
      [LIST]: [cred({ id: "c1", owner_user_id: "u1" })],
      [MEMBERS]: [],
    });
    const { container } = render(<MachineCredentials orgId={ORG} canManage />);
    await waitFor(() =>
      expect(container.querySelector('[data-state="all-owned"]')).not.toBeNull(),
    );
    // ⚠ ORDER IS THE ASSERTION. A qualifier under a list is read after the list is already believed.
    const banner = container.querySelector('[data-state="all-owned"]')!;
    const list = container.querySelector("ul")!;
    expect(banner.compareDocumentPosition(list) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("a MIXED fleet is not 'all owned' — the banner must be earned per-row", async () => {
    stubGet({
      [LIST]: [cred({ id: "c1", owner_user_id: "u1" }), cred({ id: "c2" })],
      [MEMBERS]: [],
    });
    const { container } = render(<MachineCredentials orgId={ORG} canManage />);
    await waitFor(() => expect(container.querySelectorAll("li").length).toBe(2));
    expect(container.querySelector('[data-state="all-owned"]')).toBeNull();
  });
});

describe("the row tells the truth about what it knows", () => {
  it("⛔ BOTH states of ownership — owned carries no picker, unowned does", async () => {
    stubGet({
      [LIST]: [cred({ id: "c1", owner_user_id: "u1", name: "owned-one" }), cred({ id: "c2", name: "orphan" })],
      [MEMBERS]: [{ user_id: "u1", email: "a@example.com", role: "owner", status: "active" }],
    });
    const { container } = render(<MachineCredentials orgId={ORG} canManage />);
    await waitFor(() => expect(container.querySelectorAll("li").length).toBe(2));
    // Asserting only the unowned row would make this a test about a constant.
    expect(container.querySelector('li[data-owned="yes"]')).not.toBeNull();
    expect(container.querySelector('li[data-owned="no"]')).not.toBeNull();
    // The picker exists for the unassigned one only.
    expect(screen.getByRole("combobox", { name: /owner for orphan/i })).toBeTruthy();
    expect(screen.queryByRole("combobox", { name: /owner for owned-one/i })).toBeNull();
  });

  it("⛔ NO SUGGESTED OWNER — the picker starts empty and the copy says the system does not know", async () => {
    stubGet({
      [LIST]: [cred({ id: "c2", name: "orphan" })],
      [MEMBERS]: [{ user_id: "u1", email: "a@example.com", role: "owner", status: "active" }],
    });
    render(<MachineCredentials orgId={ORG} canManage />);
    const sel = (await screen.findByRole("combobox", {
      name: /owner for orphan/i,
    })) as HTMLSelectElement;
    // A pre-selected owner would be a client-invented value where a server fact belongs.
    expect(sel.value).toBe("");
    expect(screen.getByText(/does not record who minted a credential/i)).toBeTruthy();
  });

  it("⛔ last_used_at renders as LAST SEEN and never as a liveness verdict", async () => {
    stubGet({
      [LIST]: [cred({ id: "c1", owner_user_id: "u1", last_used_at: new Date().toISOString() })],
      [MEMBERS]: [],
    });
    render(<MachineCredentials orgId={ORG} canManage />);
    await waitFor(() => expect(screen.getByText(/last seen/i)).toBeTruthy());
    // It is LAST AUTHENTICATED AT. A credential idle for a day may be an hourly reconcile or abandoned.
    for (const banned of [/\bin use\b/i, /\bactive\b/i, /\bidle\b/i, /\bonline\b/i]) {
      expect(screen.queryByText(banned)).toBeNull();
    }
  });
});
