import { describe, expect, it } from "vitest";
import {
  CAPTURE_EFFECT,
  DOMAIN_STEPS,
  KEEP_RECORD_NOTE,
  WRITE_ONLY_NOTE,
  domainErrorCopy,
  domainGate,
  chipTone,
  domainStepIndex,
  normalizeDomain,
  NO_CLAIM_CHIP,
  txtInstruction,
} from "../src/lib/domainview";

// ⛔ THE RECORD THE SERVER ACTUALLY LOOKS FOR.
//
// These two tests exist because the WIREFRAME IS WRONG ABOUT BOTH HALVES of the DNS
// instruction, and the panel is the only place the instruction appears. An operator who
// followed the design would publish at a subdomain the resolver never queries, carrying a
// prefix the comparison never matches — and would then watch verify fail forever with the
// record visibly present in their zone.
//
// The authority is `DomainService.txtHasToken`, enterprise/sso/domain.go:175:
//     records, err := s.dns.LookupTXT(ctx, domain)   // the APEX
//     want := "tunnex-verify=" + token
//     ... strings.TrimSpace(r) == want               // EXACT equality
describe("txtInstruction — measured against the resolver, not the design", () => {
  it("names the APEX, never a _tunnex-verify subdomain", () => {
    const txt = txtInstruction("acme.io", "tunnex-verify=abc123");
    expect(txt.name).toBe("acme.io");
    // The wireframe's form, pinned as WRONG so a well-meaning "match the design" edit fails
    // here instead of in a customer's DNS zone.
    expect(txt.name).not.toBe("_tunnex-verify.acme.io");
    expect(txt.name.startsWith("_")).toBe(false);
  });

  it("passes the server's value through verbatim rather than reassembling it", () => {
    // CreateClaim returns the COMPLETE value ("tunnex-verify=" + token, domain.go:118).
    // If the client ever rebuilt it from a bare token, the prefix would be a second source
    // of truth about an exact-equality comparison.
    const txt = txtInstruction("acme.io", "tunnex-verify=9c44deadbeef");
    expect(txt.value).toBe("tunnex-verify=9c44deadbeef");
    expect(txt.value).not.toContain("tnx-domain"); // the wireframe's invented prefix
  });

  it("normalizes the name the way CreateClaim does before comparing", () => {
    // domain.go:93 lowercases and trims before the lookup; a panel showing "ACME.io " while
    // the server queries "acme.io" invites a mismatch report that is not a mismatch.
    expect(txtInstruction("  ACME.io  ", "tunnex-verify=x").name).toBe("acme.io");
    expect(normalizeDomain("  ACME.io ")).toBe("acme.io");
  });
});

// ⛔ PERMISSION BEFORE EDITION. The inversion shipped twice this epic; the server's own
// ordering is authorize (sso_handlers.go:180) then edition (:183), and the client mirrors it.
describe("domainGate", () => {
  it("hides the panel from a member BEFORE considering the edition", () => {
    // The load-bearing case: open edition AND no permission. Reversed, this returns "upsell"
    // and the member is sold a capability they still would not be allowed to use.
    expect(domainGate({ role: "member", isEnterprise: false })).toEqual({
      kind: "hidden",
    });
    expect(domainGate({ role: "member", isEnterprise: true })).toEqual({
      kind: "hidden",
    });
  });

  it("upsells an admin on the open edition", () => {
    expect(domainGate({ role: "admin", isEnterprise: false })).toEqual({
      kind: "upsell",
    });
  });

  it("is ready for an admin on enterprise", () => {
    expect(domainGate({ role: "admin", isEnterprise: true })).toEqual({
      kind: "ready",
    });
    expect(domainGate({ role: "owner", isEnterprise: true })).toEqual({
      kind: "ready",
    });
  });

  it("treats an unknown role as hidden, not as permitted", () => {
    expect(domainGate({ role: null, isEnterprise: true })).toEqual({
      kind: "hidden",
    });
  });
});

// ⛔ THE UNKNOWN ARM IS FIRST-CLASS. There is no GET for domain claims, so "we have not been
// told" must not render as "CLAIMED". It sits BEFORE the chain, at -1.
describe("domainStepIndex", () => {
  it("puts unknown before the chain, not at its first pill", () => {
    expect(domainStepIndex({ kind: "unknown" })).toBe(-1);
  });

  it("puts a fresh claim at DNS-TXT PENDING, since claiming and awaiting are one round-trip", () => {
    const s = domainStepIndex({
      kind: "pending",
      domain: "acme.io",
      txt: { name: "acme.io", value: "tunnex-verify=x" },
    });
    expect(s).toBe(1);
    expect(DOMAIN_STEPS[s]).toBe("DNS-TXT PENDING");
  });

  it("puts a verified domain at the last pill", () => {
    const s = domainStepIndex({ kind: "verified", domain: "acme.io" });
    expect(s).toBe(2);
    expect(DOMAIN_STEPS[s]).toBe("VERIFIED");
  });
});

// Every code below was read off the service, not guessed. A code the server cannot emit
// would be untestable against it, which is the point of citing line numbers.
describe("domainErrorCopy", () => {
  it("tells the operator what to DO about the ownership guard", () => {
    const copy = domainErrorCopy("domain_ownership_required"); // domain.go:101
    expect(copy).toMatch(/verified account at this domain/i);
  });

  it("explains that a failed verification may be propagation, not a wrong record", () => {
    const copy = domainErrorCopy("verification_failed"); // domain.go:138
    expect(copy).toMatch(/TXT record was not found/i);
    expect(copy).toMatch(/propagate/i);
  });

  it("distinguishes a domain taken by another org from a bad request", () => {
    expect(domainErrorCopy("domain_taken")).toMatch(/another organization/i); // domain.go:200
  });

  it("covers the remaining server codes", () => {
    expect(domainErrorCopy("public_domain")).toMatch(/public email domains/i);
    expect(domainErrorCopy("invalid_domain")).toMatch(/acme\.io/);
    expect(domainErrorCopy("claim_not_found")).toMatch(/Claim it first/i);
    expect(domainErrorCopy("edition_required")).toMatch(/enterprise/i);
  });

  it("falls back without inventing a diagnosis", () => {
    expect(domainErrorCopy(null)).toBe("Could not complete the request.");
    expect(domainErrorCopy("something_new_the_server_added")).toBe(
      "Could not complete the request.",
    );
  });
});

// ⛔ THE TWO FACTS THE PILL CHAIN CANNOT CARRY. Both are pinned as WORDS, not paraphrases,
// because each one IS a disposition rendered:
//   - D3 (write-only state): there is no GET, so a claim from an earlier session is invisible.
//   - the fourth state: verification is re-checked at every signup and can be LOST
//     (capturingOrgTx -> SuspendDomainClaim, domain.go:167 / queries:26). VERIFIED is not
//     terminal, and a wireframe that ends the chain there teaches the opposite.
describe("the notes that carry what the design omits", () => {
  it("says the claim state is not readable back", () => {
    expect(WRITE_ONLY_NOTE).toMatch(/not readable back/i);
    expect(WRITE_ONLY_NOTE).toMatch(/earlier session/i);
  });

  it("warns that removing the TXT record suspends capture", () => {
    expect(KEEP_RECORD_NOTE).toMatch(/re-checked at every signup/i);
    expect(KEEP_RECORD_NOTE).toMatch(/suspends capture/i);
  });

  it("states the effect of capture where it is turned on", () => {
    expect(CAPTURE_EFFECT).toMatch(/auto-join/i);
  });
});

// ⛔ THREE EQUAL CHIPS ARE A LEGEND, NOT A STATE — the review-stack finding.
//
// The first build used two tiers (`i <= step`), so at `unknown` — the DEFAULT here, because
// there is no GET — every chip landed in the same tier and nothing said which state the org
// was in. MEASURED from the wireframe: its chips carry DESCENDING BRIGHTNESS (#A9A9A6,
// #858582, #5E5E5B) on one shared border and background, so the design always renders all
// three and separates them by tone alone.
describe("chipTone — exactly one current chip, always", () => {
  it("gives three distinct tones, not two", () => {
    // step 1 = DNS-TXT PENDING: CLAIMED is behind, VERIFIED is ahead.
    expect(chipTone(0, 1)).toBe("done");
    expect(chipTone(1, 1)).toBe("current");
    expect(chipTone(2, 1)).toBe("todo");
    // The regression in one line: `done` and `current` must not collapse together.
    expect(chipTone(0, 1)).not.toBe(chipTone(1, 1));
  });

  it("marks exactly one chip current for every reachable state", () => {
    // Including -1: the leading NO CLAIM chip sits at index -1 and takes the current slot,
    // which is what stops the unknown render from being an unanchored legend.
    for (const step of [-1, 0, 1, 2]) {
      const tones = [-1, 0, 1, 2].map((i) => chipTone(i, step));
      expect(tones.filter((t) => t === "current")).toHaveLength(1);
    }
  });

  it("anchors the unknown state on its own chip rather than dimming everything", () => {
    expect(chipTone(-1, -1)).toBe("current");
    expect(chipTone(0, -1)).toBe("todo");
    expect(chipTone(2, -1)).toBe("todo");
    expect(NO_CLAIM_CHIP).toMatch(/no claim/i);
  });

  it("never marks a later step done because an earlier one is", () => {
    expect(chipTone(2, 0)).toBe("todo");
    expect(chipTone(1, 0)).toBe("todo");
  });
});
