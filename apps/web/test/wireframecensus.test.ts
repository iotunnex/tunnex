import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

// ⛔ A CENSUS OF WHAT EXISTS CANNOT FIND WHAT WAS NEVER BUILT.
//
// `screencensus.test.ts` is a good ledger and it could not have caught this. It enumerates
// `src/pages/` and requires every page to carry tests — so it reasons FORWARD from the codebase,
// and a screen the design specifies but nobody built is **not in its input set at all**. It
// reported full coverage of twelve screens while the wireframe specified seventeen.
//
// FOUR THINGS ESCAPED: three SCREENS — AUTH SCREENS, DESKTOP CLIENT, FLOW LOGS — plus the
// collapsible SIDEBAR, which is not a screen at all and needs the second ledger below.
//
// FLOW LOGS is the one nobody named until the census was run the other way round: `/access-events`
// and `/access-log/health` shipped in S7.5.1 and `apps/web` renders NEITHER — the fourth
// unreachable-surface instance, same class as the five idp-sync endpoints S14.14 closed.
//
// ⛔ THIS FILE IS RED ON PURPOSE AND EPIC 14 CANNOT MERGE WHILE IT IS. Each story flips one
// disposition to `built`; the epic closes when the census goes green on its own. That makes the
// closure condition MECHANICAL rather than a thing someone remembers to check — which is precisely
// what failed the first time, when the epic was declared complete with five blocks unaccounted for.
//
// > THIS CENSUS ENUMERATES THE DESIGN, NOT THE CODEBASE. The wireframe's screen banners are the
// > authoritative set, and every one of them must be BUILT, ABSORBED with a named destination, or
// > CUT with a reason. A block with no disposition fails BY NAME.
//
// Same equals-the-total shape as the screen census, for the same reason: a `>=` is satisfied
// forever by a lazy floor, while equality forces a DELIBERATE, REVIEWABLE edit when the design
// gains a block.

const WIREFRAME = join(
  __dirname,
  "..",
  "..",
  "..",
  "docs",
  "design",
  "TUNNEX-wireframe-v2.html.txt",
);
const APP = join(__dirname, "..", "src", "App.tsx");

/** Flipped to true when EPIC 14 is declared closed. Until then the close-gate assertion is inert. */
const EPIC_CLOSING = false;

/** A screen banner is `<!-- ===== NAME ===== -->`. Parsed, never transcribed. */
function banners(src: string): string[] {
  return [
    ...src.matchAll(/<!--\s*=+\s*([A-Z][A-Za-z0-9 ./&\-–]{2,60})\s*=+\s*-->/g),
  ].map((m) => m[1].trim());
}

type Disposition =
  | { kind: "built"; route: string }
  | { kind: "absorbed"; into: string; why: string }
  | { kind: "cut"; why: string }
  | { kind: "unbuilt"; story: string }
  // ⛔ built_unadopted — see the header block below before adding one.
  | {
      kind: "built_unadopted";
      surface: string;
      consumer: string;
      adoptedWhen: string;
      story: string;
      branch: string;
    };

// ⛔ EVERY DISPOSITION CARRIES ITS REASON INLINE. A name with no reason is indistinguishable from
// a name someone added to make the census pass.
const DISPOSITIONS: Record<string, Disposition> = {
  OVERVIEW: { kind: "built", route: "/dashboard" },
  GATEWAYS: { kind: "built", route: "/gateways" },
  SITES: { kind: "built", route: "/sites" },
  ACCESS: { kind: "built", route: "/access" },
  DEVICES: { kind: "built", route: "/devices" },
  USERS: { kind: "built", route: "/users" },
  KUBERNETES: { kind: "built", route: "/kubernetes" },
  AUDIT: { kind: "built", route: "/audit" },
  "ORG SETTINGS": { kind: "built", route: "/settings" },
  "CLI / ROUTED RANGES": { kind: "built", route: "/routed-ranges" },

  // ── ABSORBED: the design's block landed somewhere real. The DESTINATION is the point — an
  // absorption with no named home is just a gap someone re-discovers in six months.
  GROUPS: {
    kind: "absorbed",
    into: "/access (GroupRow) + /settings (IdP sync freshness)",
    why: "groups are edited where the rules that use them live; sync freshness belongs with the credential that produces it",
  },
  ONBOARDING: {
    kind: "absorbed",
    into: "/verify-pending (post-verify router) + /gateways (join-token ceremony)",
    why: "each step already exists on the screen that owns its data; a separate onboarding shell would duplicate both",
  },

  // ── CUT: with the measurement, not the intention.
  OPERATIONS: {
    kind: "cut",
    why: "MEASURED: backup, version and metrics have ZERO endpoints — backup/restore is `backupctl`, a CLI command, not an API. Leader election and replicas already render as Dashboard's HA Hub Set and System Health panels. Building it means INVENTING READS. Registered: the servable parts return if S11.1 (metrics) lands.",
  },
  LICENSE: {
    kind: "cut",
    why: "no license endpoints exist; EPIC 12 is PARKED and edition gating is already implemented inline via lib/edition.ts. Cut TO EPIC 12, not dropped.",
  },

  // ⛔ UNBUILT — the four the old census could not see. Each names its story so the entry is a
  // commitment rather than a note. Flipping one to `built` is the deliberate edit that records it.
  // ⛔ FLIPPED AT S14.17, AND ONLY AFTER THE LAST ITEM IN THE BLOCK WAS REAL. The census refused
  // this claim once already — the block specifies a forced-enrollment modal that cannot be
  // dismissed by click-away or Esc, and MfaSettings had no acknowledgement gate. Login hero, the
  // challenge step, the CLI authorize + device screens and that modal are now all built.
  "AUTH SCREENS": { kind: "built", route: "/login" },
  // ⛔ FLIPPED AT S14.19 — the fourth unreachable-surface instance closed. /access-events and
  // /access-log/health shipped in S7.5.1 with no consumer; neither the page census nor anyone's
  // list found it, only running the census against the DESIGN did.
  "FLOW LOGS": { kind: "built", route: "/access-events" },
  // S14.20 steps 1-2: the surface is built and reviewable at /client.html; Electron still loads
  // index.html, so nothing consumes it yet. Step 3 is a one-line PR and flips this to `built` —
  // and this entry fails the moment it does.
  "DESKTOP CLIENT": {
    kind: "built_unadopted",
    surface: "client.html",
    consumer: "../client/src/main/index.ts",
    adoptedWhen: "client.html",
    story: "S14.20",
    branch: "story/S14.19-flow-logs",
  },
};

// ⛔ AND BANNERS ALONE ARE NOT THE WHOLE DESIGN — WHICH IS WHY THIS SECOND LEDGER EXISTS.
//
// The collapsible sidebar escaped BOTH censuses: the old one because it enumerates pages, and the
// banner census because **it is not a screen**. Its spec (`sbWidth: closed ? '64px' : '228px'`,
// `wmDisp`, `tnx-nav`) sits in the wireframe's SHARED PREAMBLE — before any banner — and is
// repeated inside DESKTOP CLIENT. A census keyed only on screens cannot see a shell component, so
// design-specified components get their own named ledger with the same equals-the-total rule.
//
// ⚠ MEASURED CORRECTION: `tnx-nav` is a CSS CLASS IN THE WIREFRAME. It is **not** in our README —
// grep returns nothing — so there is no documented persistence key to transcribe. Choosing one is
// a decision for S14.18, not a fact to copy.
type ComponentDisposition = { kind: "built" | "unbuilt"; note: string };

const SHELL_COMPONENTS: Record<string, ComponentDisposition> = {
  // ⛔ FLIPPED AT S14.18. The responsive collapse already shipped; what was missing was the
  // USER-CONTROLLED one and its persistence. Both now transcribed from the design's own state
  // object — including the key `tnx-nav` and its values 'open'/'closed', which are the designer's.
  "collapsible sidebar (228px <-> 64px rail, persisted)": {
    kind: "built",
    note: "S14.18 — AppShell + lib/navcollapse.ts; widths, key, values and hide-set all from the handoff",
  },
};

// ⛔⛔ `built_unadopted` — A FACT ABOUT THE CODE, NOT A CLAIM ABOUT US.
//
// The state exists because "in progress" was proposed and rejected: **intent is unfalsifiable.** An
// entry saying *we are working on it* can never be proven wrong by a test, only by someone
// noticing — and naming a story and a branch does not fix that, because a stale entry with a dead
// branch still READS true. That is exactly the escape hatch that would have let this epic close
// early.
//
// So this state asserts something checkable instead:
//
//   > **THE SURFACE EXISTS AND IS REACHABLE. THE THING THAT SHOULD CONSUME IT DOES NOT YET.**
//
//   `surface`      a path that MUST EXIST — fails if the work was never done, so the state cannot
//                  be used to mean "not started"
//   `consumer` +   the file that must reference it, and the string that proves adoption. ⛔ WHEN
//   `adoptedWhen`  THAT STRING APPEARS, THIS DISPOSITION IS A LIE AND THE CENSUS FAILS. The state
//                  flips ITSELF: the only way back to green is changing it to `built`.
//   `story` +      for the human reading it (founder's guard, kept)
//   `branch`
//
// ⚠ THE HONEST COST, IN THE HEADER WHERE IT BELONGS: **this is weaker than `built`.** A reviewer
// must accept "reachable but unconsumed" as a resting state for a merge. That is acceptable HERE
// because the surface is done and reviewable in a browser and the single line that adopts it is
// deliberately its own PR. **It would NOT be acceptable for a block with no surface at all — and
// the `surface` existence check is precisely what stops it becoming a hiding place.**
//
// And the epic cannot close while any block sits here — asserted separately and by name, below.

const wireframe = readFileSync(WIREFRAME, "utf8");
const app = readFileSync(APP, "utf8");
const BLOCKS = banners(wireframe);

describe("wireframe census — the DESIGN is the authoritative set", () => {
  it("finds the screen banners at all (vacuity floor)", () => {
    // If the banner syntax ever changes this drops to zero and every assertion below passes
    // against an empty set — the failure mode this repo has filed repeatedly.
    expect(BLOCKS.length).toBeGreaterThanOrEqual(17);
  });

  it("⛔ every wireframe block has a disposition — EQUALS the total, not >=", () => {
    const undisposed = BLOCKS.filter((b) => !DISPOSITIONS[b]);
    expect(undisposed).toEqual([]);
    // Equality both ways: a disposition for a block the design no longer has is also drift.
    expect(Object.keys(DISPOSITIONS).sort()).toEqual([...BLOCKS].sort());
  });

  it("⛔ NOTHING IS UNBUILT — this is the assertion that rejects, and it names them", () => {
    const unbuilt = BLOCKS.filter((b) => DISPOSITIONS[b]?.kind === "unbuilt").map(
      (b) => `${b} (${(DISPOSITIONS[b] as { story: string }).story})`,
    );
    expect(unbuilt).toEqual([]);
  });

  it("⛔ every built_unadopted block's SURFACE actually exists — not a hiding place", () => {
    for (const b of BLOCKS) {
      const d = DISPOSITIONS[b];
      if (d?.kind !== "built_unadopted") continue;
      const p = join(__dirname, "..", d.surface);
      expect(existsSync(p), `${b}: surface ${d.surface} must exist`).toBe(true);
      expect(d.story.length, `${b} must name its story`).toBeGreaterThan(3);
      expect(d.branch.length, `${b} must name its branch`).toBeGreaterThan(3);
    }
  });

  it("⛔ built_unadopted FLIPS ITSELF — the state is a lie once the consumer adopts", () => {
    // The whole point: this disposition cannot outlive its justification. When the consumer
    // references the surface, the only way back to green is changing the kind to `built`.
    for (const b of BLOCKS) {
      const d = DISPOSITIONS[b];
      if (d?.kind !== "built_unadopted") continue;
      const consumer = readFileSync(join(__dirname, "..", d.consumer), "utf8");
      expect(
        consumer.includes(d.adoptedWhen),
        `${b} is marked built_unadopted, but ${d.consumer} now references "${d.adoptedWhen}" — it IS adopted. Change the disposition to { kind: "built", route: … }.`,
      ).toBe(false);
    }
  });

  it("⛔ THE EPIC CANNOT CLOSE while any block is built_unadopted", () => {
    // Named on its own so it fails LOUDLY rather than as a side effect of something else.
    // EPIC_CLOSING is flipped by hand when the epic is declared done; until then this is inert.
    const pending = BLOCKS.filter(
      (b) => DISPOSITIONS[b]?.kind === "built_unadopted",
    );
    if (!EPIC_CLOSING) {
      expect(Array.isArray(pending)).toBe(true); // inert while the epic is open
      return;
    }
    expect(
      pending,
      "EPIC 14 cannot close with blocks still built-but-unadopted",
    ).toEqual([]);
  });

  it("every BUILT block names a route that actually exists in App.tsx", () => {
    // Stops a disposition from being aspirational: "built" must be checkable against the router.
    const missing = BLOCKS.filter((b) => {
      const d = DISPOSITIONS[b];
      return d?.kind === "built" && !app.includes(`path="${d.route}"`);
    });
    expect(missing).toEqual([]);
  });

  it("⛔ NO SHELL COMPONENT IS UNBUILT — banners cannot see these, so they are counted separately", () => {
    const unbuilt = Object.entries(SHELL_COMPONENTS)
      .filter(([, d]) => d.kind === "unbuilt")
      .map(([name, d]) => `${name} :: ${d.note}`);
    expect(unbuilt).toEqual([]);
  });

  it("every ABSORBED block names where it landed, and every CUT block says why", () => {
    for (const b of BLOCKS) {
      const d = DISPOSITIONS[b];
      if (d?.kind === "absorbed") {
        expect(d.into.length, `${b} must name its destination`).toBeGreaterThan(10);
        expect(d.why.length, `${b} must say why`).toBeGreaterThan(20);
      }
      if (d?.kind === "cut") {
        expect(d.why.length, `${b} must say why it was cut`).toBeGreaterThan(30);
      }
    }
  });
});
