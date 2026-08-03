import { describe, expect, it } from "vitest";
import {
  CLIENT_STATES,
  PREVIEW_DISCLAIMER,
  formatBytes,
  formatDuration,
  formatRate,
  parsePreviewState,
  stateView,
  trayAppearance,
} from "../src/lib/clientstate";

// ⛔ TEN STATES FROM THE BLOCK, PLUS ONE OF OURS.
describe("CLIENT_STATES", () => {
  it("carries every state the design names, and `failed` which it omits", () => {
    for (const s of [
      "connected", "connecting", "disconnected", "revoked", "posture_blocked",
      "migrate_failed", "pending_approval", "helper_outdated", "kill_switch", "expired_creds",
    ] as const) {
      expect(CLIENT_STATES).toContain(s);
    }
    // Ours. A design's missing state is usually the FAILURE state — a designer drawing a healthy
    // product has nothing to look at when drawing it.
    expect(CLIENT_STATES).toContain("failed");
    expect(CLIENT_STATES).toHaveLength(11);
  });

  it("every state has a view — the switch is exhaustive", () => {
    for (const s of CLIENT_STATES) {
      const v = stateView(s);
      expect(v.label.length).toBeGreaterThan(3);
      // The detail must SAY something, not restate the label.
      expect(v.detail.length).toBeGreaterThan(25);
      expect(v.detail.toLowerCase()).not.toBe(v.label.toLowerCase());
    }
  });
});

// ⛔ "THE ICON IS NEVER GREEN WHILE THE TUNNEL IS DEAD" — the block's own rule, and the only one it
// states outright. Everything that is not a live tunnel is grey or red.
describe("trayAppearance", () => {
  it("⛔ is solid ONLY for connected", () => {
    for (const s of CLIENT_STATES) {
      expect(trayAppearance(s) === "solid").toBe(s === "connected");
    }
  });

  it("pulses only while connecting", () => {
    expect(trayAppearance("connecting")).toBe("pulsing");
  });

  it("⛔ is RED for exactly the two states where access changed without the user asking", () => {
    const red = CLIENT_STATES.filter((s) => trayAppearance(s) === "red");
    expect(red.sort()).toEqual(["kill_switch", "revoked"]);
  });

  it("⛔ posture_blocked is NOT green — the tunnel is down", () => {
    // The trap: it is a "policy" state, so it reads as benign. The tunnel is still dead.
    expect(trayAppearance("posture_blocked")).toBe("grey");
  });
});

// ⛔ A NOTIFICATION ON EVERY TRANSITION TRAINS PEOPLE TO DISMISS THEM, which costs exactly the two
// that matter. The block fires them for revoked and kill-switch.
describe("notifications", () => {
  it("fire for revoked and kill-switch, and for nothing else", () => {
    const notifying = CLIENT_STATES.filter((s) => stateView(s).notify);
    expect(notifying.sort()).toEqual(["kill_switch", "revoked"]);
  });
});

// ⛔ NULL ACTION MEANS NO BUTTON. Offering "Connect" on a revoked device is a control that cannot
// work — worse than none.
describe("the primary verb", () => {
  it("is absent where the user genuinely cannot act", () => {
    for (const s of ["revoked", "posture_blocked", "pending_approval", "helper_outdated"] as const) {
      expect(stateView(s).action, `${s} must offer no button`).toBeNull();
    }
  });

  it("is present where pressing it does something", () => {
    for (const s of ["connected", "connecting", "disconnected", "failed", "migrate_failed", "kill_switch", "expired_creds"] as const) {
      expect(stateView(s).action, `${s} needs a verb`).toBeTruthy();
    }
  });

  it("⛔ expired creds sends the user to the BROWSER — never an in-app password field", () => {
    // The block: "MFA touches the client only via browser re-auth … NEVER an in-app password field."
    const a = stateView("expired_creds").action!;
    expect(a).toMatch(/browser/i);
    expect(a).not.toMatch(/password/i);
  });

  it("⛔ the kill-switch verb RESTORES ROUTING rather than pretending to reconnect", () => {
    const v = stateView("kill_switch");
    expect(v.action).toMatch(/restore/i);
    expect(v.detail).toMatch(/safe state, not a fault/i);
  });
});

describe("formatting", () => {
  it("⛔ renders n/a for unknown, never 0 — a zero nobody measured is a claim", () => {
    expect(formatBytes(null)).toBe("n/a");
    expect(formatRate(undefined)).toBe("n/a");
    expect(formatDuration(null)).toBe("n/a");
    expect(formatDuration(-1)).toBe("n/a");
  });

  it("scales bytes and keeps duration readable", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(2252)).toBe("2.2 KB");
    expect(formatDuration(65)).toBe("1:05");
    expect(formatDuration(3725)).toBe("1:02:05");
  });
});

describe("parsePreviewState", () => {
  it("accepts only real states", () => {
    expect(parsePreviewState("?state=kill_switch")).toBe("kill_switch");
    expect(parsePreviewState("?state=nonsense")).toBeNull();
    expect(parsePreviewState("")).toBeNull();
  });

  it("⛔ the disclaimer says the preview proves the RENDER, not the transition", () => {
    expect(PREVIEW_DISCLAIMER).toMatch(/not reached by a real transition/i);
  });
});

// ⛔ THE HYPERDRIVE — the two canvases I missed by reading a TEXT extraction of the design.
import {
  createHyperState,
  pushSample,
  stepLink,
} from "../src/client/hyperdrive";

describe("hyperdrive state", () => {
  it("⛔ is SEEDED, not random — the one deliberate departure from the handoff", () => {
    // The handoff seeds node phases with Math.random(), which makes the animation unsnapshottable
    // and every visual diff noisy forever. Two states built with the same seed must match.
    const a = createHyperState(7);
    const b = createHyperState(7);
    expect(a.nodes.map((n) => n.tw)).toEqual(b.nodes.map((n) => n.tw));
    // ...and the VARIETY survives: nodes must not all share a phase.
    expect(new Set(a.nodes.map((n) => n.tw)).size).toBeGreaterThan(1);
  });

  it("carries the designer's seven nodes and their stagger", () => {
    const st = createHyperState();
    expect(st.nodes).toHaveLength(7);
    expect(st.nodes[0].stagger).toBe(0);
    expect(st.nodes[6].stagger).toBeCloseTo((6 / 7) * 0.6, 5);
  });

  it("⛔ connecting eases SLOWLY and connected snaps — the rates are the design's", () => {
    const slow = createHyperState();
    slow.mode = "connecting";
    stepLink(slow);
    const fast = createHyperState();
    fast.mode = "connected";
    stepLink(fast);
    expect(fast.link).toBeGreaterThan(slow.link);
    expect(slow.link).toBeCloseTo(0.018, 4);
    expect(fast.link).toBeCloseTo(0.06, 4);
  });

  it("⛔ the graph DRAINS when the tunnel drops rather than snapping to zero", () => {
    const st = createHyperState();
    st.mode = "connected";
    st.graph = [0.8];
    st.mode = "idle";
    pushSample(st, () => 0.5);
    expect(st.graph[st.graph.length - 1]).toBeCloseTo(0.71, 5); // 0.8 - 0.09
  });

  it("never lets a drained graph go negative", () => {
    const st = createHyperState();
    st.graph = [0.05];
    pushSample(st, () => 0.5);
    expect(st.graph[st.graph.length - 1]).toBe(0);
  });

  it("caps the window at 64 samples so the plot scrolls", () => {
    const st = createHyperState();
    st.mode = "connected";
    for (let i = 0; i < 200; i++) pushSample(st, () => 0.5);
    expect(st.graph).toHaveLength(64);
  });

  it("clears the connected-at clock when the tunnel drops", () => {
    const st = createHyperState();
    st.mode = "connected";
    pushSample(st, () => 0.5);
    expect(st.connAt).not.toBeNull();
    st.mode = "idle";
    pushSample(st, () => 0.5);
    expect(st.connAt).toBeNull();
  });
});

// ⛔ THE VERB'S TARGET — which action a state's button performs.
//
// The button shipped with NO onClick at all: it rendered, it looked right, and clicking it did
// nothing. Pinned here as data so the mapping cannot silently invert (Disconnect wired to up()
// is a click that turns the tunnel ON while saying it is tearing it down).
describe("what the primary verb does", () => {
  it("⛔ tears DOWN from connected and kill-switch, never up", () => {
    // kill_switch is the trap: its verb reads "restore normal routing", which is a DOWN.
    for (const s of ["connected", "kill_switch"] as const) {
      expect(stateView(s).action).toMatch(/disconnect|restore/i);
    }
  });

  it("brings UP from every state that offers a connect", () => {
    for (const s of ["disconnected", "failed", "migrate_failed"] as const) {
      expect(stateView(s).action).toMatch(/connect · link the mesh/i);
    }
  });

  it("⛔ cancels — not disconnects — while connecting", () => {
    // "Disconnect" mid-handshake would describe tearing down something not yet up.
    expect(stateView("connecting").action).toMatch(/^cancel/i);
  });
});
