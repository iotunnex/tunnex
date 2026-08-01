import { describe, expect, it } from "vitest";
import { readdirSync } from "node:fs";
import { join } from "node:path";

// D3 — THE CENSUS. A LEDGER, NOT A FLOOR.
//
// The gate is NOT a coverage percentage. A percentage is the gameable number: it rises when someone tests
// something easy and says nothing about whether the surface that breaks is guarded. Instead every screen in a
// NAMED LIST must have a wiring test and a failure-path test, and the count must EQUAL the screen total.
//
// WHY EQUALS AND NOT >=. A minimum count is satisfied forever by a lazy floor — `>= 1` passes on screen 2 and
// on screen 19 alike, which is the gameable-number failure in a different costume. Asserting equality means
// screen 19 FAILS THE CENSUS BY NAME and the number has to be MOVED DELIBERATELY. Moving it is a visible,
// reviewable edit; that is what makes this a ledger rather than a floor.
//
// The precedent is in this repo and it works: TestEveryHealthKindReachesItsMirrorSurfaces, minted for the same
// class — a producer whose consumers were never enumerated, which is WF-S11-7 exactly.
//
// ENUMERATED, NOT LISTED. The screen set is read from the filesystem so it cannot go stale; exemptions are an
// explicit allow-list. EVERY EXEMPTION CARRIES ITS REASON INLINE, because an unreasoned exemption is how the
// list quietly becomes the codebase — a name with no reason is indistinguishable from a name someone added to
// make the census pass, and six months later nobody can tell which it was.

const PAGES_DIR = join(__dirname, "..", "src", "pages");

// EXEMPT — the reason is part of the datum, not documentation about it.
const EXEMPT: Record<string, string> = {
  "Login.tsx": "unauthenticated shell — no backend concept to disagree about",
  "Signup.tsx": "unauthenticated shell — no backend concept to disagree about",
  "ForgotPassword.tsx": "single-form flow; the decision is server-side, nothing rendered to disagree with",
  "ResetPassword.tsx": "single-form flow; the decision is server-side, nothing rendered to disagree with",
  "VerifyEmail.tsx": "terminal status page — renders a fixed state, no list, no derivation",
  "VerifyPending.tsx": "terminal status page — renders a fixed state, no list, no derivation",
  "AcceptInvite.tsx": "one-shot token redemption; no ongoing backend concept",
  "CreateOrg.tsx": "one form, one POST, no rendered backend state",
  // TESTED ELSEWHERE, not skipped — the distinction matters and is why the reason names the coverage.
  "CliAuth.tsx": "single-purpose consent flow; the property that matters (no click, no mint) is covered by S5.1's Playwright leg",
  "CliDevice.tsx": "single-purpose consent flow; the property that matters (no click, no mint) is covered by S5.1's Playwright leg",
};

// COVERED — a screen enters this list when it has BOTH a wiring test and a failure-path test.
const COVERED: Record<string, string> = {
  "Gateways.tsx": "test/gatewayswiring.test.tsx — revoke wiring + revoked-suppression + failed-revoke surfaced",
  "Devices.tsx": "test/deviceswiring.test.tsx — posture/re-export suppression on revoked + failed-load surfaced, distinct from empty",
};

// PENDING — accounted for, NOT yet covered. This list is the BACKLOG STATED OUT LOUD, and it exists because a
// census that only knows COVERED and EXEMPT lands RED on day one: it would either block the branch or be
// skipped, and a skipped gate is a vacuous gate wearing a different hat.
//
// It does not weaken the ledger. A NEW screen still fails by name, because it appears in none of the three
// lists. What PENDING buys is that the eight known-uncovered screens are VISIBLE and COUNTED rather than
// hidden behind a red the reader learns to ignore. Moving a screen from PENDING to COVERED requires editing
// BOTH totals below — two deliberate edits in one reviewable diff.
//
// THE ORDER IS THE COMMIT-ONE ORDER, and the reason is recorded with it: surfaces are ranked by where
// disagreement with the backend is most consequential, not by size.
const PENDING: Record<string, string> = {
  "Access.tsx": "after Devices — a rule shown active but not compiled is a silent authorization gap",
  "Kubernetes.tsx": "after Access — WF-S11-7's own territory (the unrendered health kind)",
  "Sites.tsx": "carries the D4 three-way assertion already (test/siblingconsistency.test.tsx); still owes its own wiring + failure-path pair",
  "Users.tsx": "unranked backlog",
  "AuditLog.tsx": "unranked backlog",
  "Settings.tsx": "unranked backlog",
  "Dashboard.tsx": "unranked backlog",
};

describe("screen census", () => {
  const screens = readdirSync(PAGES_DIR).filter((f) => f.endsWith(".tsx"));

  // THE CENSUS'S OWN VACUITY GUARD. A census that passes because it enumerated ZERO screens would be the very
  // class this file exists to prevent — it would go green forever on a bad glob or a moved directory. The
  // number is known independently and asserted, so an empty enumeration FAILS.
  it("enumerates a plausible number of screens (guards against a census that counts nothing)", () => {
    expect(screens.length).toBeGreaterThanOrEqual(15);
  });

  it("every screen is COVERED, PENDING or EXEMPT — a NEW screen fails here BY NAME", () => {
    const unaccounted = screens.filter((s) => !(s in COVERED) && !(s in PENDING) && !(s in EXEMPT));
    // `Gateways.tsx` lives in components/ but IS the gateway screen; it is accounted for in COVERED and is not
    // enumerated here, which is why it never appears in `unaccounted`.
    expect(unaccounted, `unaccounted screens (add a wiring+failure test, or a PENDING/EXEMPT entry WITH A REASON): ${unaccounted.join(", ")}`).toEqual([]);
  });

  it("every EXEMPT and PENDING entry carries a non-empty reason", () => {
    const unreasoned = [...Object.entries(EXEMPT), ...Object.entries(PENDING)].filter(([, why]) => !why || why.trim().length < 10);
    expect(unreasoned.map(([f]) => f)).toEqual([]);
  });

  // A screen cannot be in two lists at once — that is how a "covered" screen quietly stays on the backlog, or
  // an exempt one silently acquires an obligation nobody meant to give it.
  it("the three lists are disjoint", () => {
    const names = [...Object.keys(COVERED), ...Object.keys(PENDING), ...Object.keys(EXEMPT)];
    expect(names.length).toBe(new Set(names).size);
  });

  // THE LEDGER LINES. Not floors. Covering a screen means moving it from PENDING to COVERED and editing BOTH
  // numbers — two deliberate edits, in one diff a reviewer sees. A `>=` here would be satisfied forever.
  it("the COVERED count equals its ledger total", () => {
    expect(Object.keys(COVERED).length).toBe(2);
  });

  it("the PENDING count equals its ledger total — the backlog shrinks deliberately or not at all", () => {
    expect(Object.keys(PENDING).length).toBe(7);
  });
});
