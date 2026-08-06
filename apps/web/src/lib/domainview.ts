// Domain capture — the view-model.
//
// ⛔ THE SERVER SERVES NO READ, AND THAT IS THE WHOLE DESIGN PROBLEM ON THIS PANEL.
//
// The spec has `POST /organizations/{orgId}/domains` (createDomainClaim) and
// `POST /organizations/{orgId}/domains/verify` (verifyDomainClaim) — and NO GET
// (openapi.yaml:1793 and :1817; the whole block, checked). `domain_claims` is real,
// persistent, org-scoped state, and `ListDomainClaims` is already WRITTEN in
// `db/queries/domain_claims.sql:10` — it is simply not exposed over HTTP.
//
// So the state a claim is in is knowable ONLY inside the session that wrote it. Per D3
// (ruled: register, do not build reads) this file does NOT invent one. What it does
// instead is make the panel say so, because the alternative is worse than a gap:
//
//   A THREE-STATE PILL CHAIN DRAWN OVER A STATE NOBODY CAN READ IS A CLAIM ABOUT THE
//   SERVER, AND ON RELOAD IT SILENTLY BECOMES A LIE ABOUT IT.
//
// The wireframe draws exactly that chain (CLAIMED -> DNS-TXT PENDING -> VERIFIED). It is a
// picture of readable state. We render the chain — it is the right operator model — but the
// unknown arm is FIRST-CLASS and named, not the absence of the other three.

import type { Role } from "./api";
import { can } from "./rbac";

/**
 * What we know about the org's domain claim, this session.
 *
 * `unknown` is NOT "no claim exists" — it is "the server offers no way to ask". Those are
 * different facts and the panel must not print the first when it means the second. Same
 * distinction the SSO section was fixed for one commit earlier: not-configured is a state,
 * an unanswerable question is not.
 */
export type DomainClaimState =
  | { kind: "unknown" }
  | { kind: "pending"; domain: string; txt: TxtInstruction }
  | { kind: "verified"; domain: string };

/** The DNS record the operator must publish, as the SERVER will look for it. */
export type TxtInstruction = { name: string; value: string };

/**
 * ⛔ MEASURED AGAINST THE RESOLVER, AND THE WIREFRAME IS WRONG ABOUT BOTH HALVES.
 *
 * `DomainService.txtHasToken` (enterprise/sso/domain.go:175) does:
 *
 *     records, err := s.dns.LookupTXT(ctx, domain)      // <- THE APEX. No prefix.
 *     want := "tunnex-verify=" + token
 *     ... strings.TrimSpace(r) == want                  // <- EXACT equality
 *
 * The wireframe instructs `_tunnex-verify.acme.io TXT "tnx-domain-9c44…"`. Both the record
 * NAME and the record VALUE differ from what the server queries and compares:
 *
 *   name   wireframe `_tunnex-verify.acme.io`   server looks up `acme.io`
 *   value  wireframe `tnx-domain-9c44…`         server wants `tunnex-verify=<token>`
 *
 * An operator who followed the design would publish a record at a subdomain the resolver
 * never reads, carrying a prefix the comparison never matches, and would then watch verify
 * fail forever with the record visibly present in their zone. THE PANEL IS THE ONLY PLACE
 * THIS INSTRUCTION APPEARS, so it is the only place that can be wrong about it.
 *
 * `createDomainClaim` returns the COMPLETE value (`"tunnex-verify=" + token`,
 * domain.go:118), so the client never assembles it and cannot drift from the comparison.
 */
export function txtInstruction(
  domain: string,
  txtRecord: string,
): TxtInstruction {
  return { name: normalizeDomain(domain), value: txtRecord };
}

/** Lowercase + trim, matching `CreateClaim`'s own normalization (domain.go:93). */
export function normalizeDomain(domain: string): string {
  return domain.trim().toLowerCase();
}

/**
 * The gate. PERMISSION FIRST, THEN EDITION — the ordering law from S14.11/S14.12, and it
 * matches the server: `CreateDomainClaim` runs `authorize(..., PermOrgUpdate)` at
 * sso_handlers.go:180 and only then checks `s.sso == nil` for `edition_required` (:183).
 *
 * Reversed, an open-edition MEMBER is told to buy enterprise for a capability they would
 * still not be allowed to use. That exact inversion shipped twice this epic.
 */
export type DomainGate =
  { kind: "hidden" } | { kind: "upsell" } | { kind: "ready" };

export function domainGate(i: {
  role: Role | null;
  isEnterprise: boolean;
}): DomainGate {
  if (!i.role || !can(i.role, "org:update")) return { kind: "hidden" };
  if (!i.isEnterprise) return { kind: "upsell" };
  return { kind: "ready" };
}

/** The wireframe's pill chain, as an index. `unknown` sits BEFORE the chain, at -1. */
export const DOMAIN_STEPS = ["CLAIMED", "DNS-TXT PENDING", "VERIFIED"] as const;

/**
 * ⛔ THREE EQUAL CHIPS ARE A LEGEND, NOT A STATE — found on the review stack.
 *
 * All three pills rendered beside the heading with nothing marking which one the org is in.
 * MEASURED from the wireframe rather than guessed: its three chips carry DESCENDING
 * BRIGHTNESS — `#A9A9A6`, `#858582`, `#5E5E5B` — on one shared border and background. So the
 * design always renders all three and distinguishes them by TONE ALONE. The first build
 * collapsed that to two tiers (`i <= step`), which at `unknown` made all three identical.
 *
 * Three tones, so exactly one chip is ever "current":
 *   done    — passed, behind us
 *   current — where this org is now
 *   todo    — not yet reached
 *
 * ⚠ AND TONE IS NOT ENOUGH ON ITS OWN. The wireframe encodes the whole distinction in colour;
 * a colour-blind operator, or anyone on a washed-out display, gets the legend back. The
 * renderer pairs each tone with a non-colour cue (a mark on `done`, a ring plus
 * `aria-current="step"` on `current`) — the design's tones are honoured, not obeyed literally.
 */
export type ChipTone = "done" | "current" | "todo";

export function chipTone(i: number, step: number): ChipTone {
  if (i < step) return "done";
  if (i === step) return "current";
  return "todo";
}

/**
 * ⛔ AND `unknown` STILL HAS NO SUBJECT, because -1 is not one of the three.
 *
 * With no GET, "we have not been told" is the DEFAULT state of this panel, not an edge case —
 * so the most common render was the one with no current chip at all. Rather than leave the
 * chain unanchored, `unknown` gets its own leading chip and becomes the current step. **There
 * is now always exactly one current chip**, which is the property that stops it reading as a
 * legend.
 */
export const NO_CLAIM_CHIP = "NO CLAIM THIS SESSION";

export function domainStepIndex(s: DomainClaimState): number {
  switch (s.kind) {
    case "unknown":
      return -1;
    case "pending":
      return 1; // claimed AND awaiting the TXT — the wireframe's middle pill
    case "verified":
      return 2;
  }
}

/**
 * ⛔ THE HONEST LABEL FOR THE UNREADABLE STATE. Named as a constant so the test pins the
 * words, not a paraphrase — this sentence IS the D3 disposition, rendered.
 */
export const WRITE_ONLY_NOTE =
  "Claims are set, not readable back — this server serves no read for domain claims, so a claim made in an earlier session will not appear here. Re-running Verify is safe and tells you the live answer.";

/** What capture actually does, stated where the operator turns it on. */
export const CAPTURE_EFFECT =
  "Once verified, new signups on this domain auto-join this org.";

/**
 * ⛔ THE FOURTH STATE, WHICH THE WIREFRAME DOES NOT DEPICT: verification is RE-CHECKED on
 * every capture, and losing the TXT record SUSPENDS the claim.
 *
 * `capturingOrgTx` (domain.go:167) re-resolves the record at signup time and calls
 * `SuspendDomainClaim` when it is gone — which sets `verified_at = NULL` and leaves the row
 * pending (queries/domain_claims.sql:26). So VERIFIED is not terminal, and an operator who
 * "cleans up" the TXT record silently turns capture off for everyone. A screen that draws
 * VERIFIED as the end of a chain teaches the opposite.
 */
export const KEEP_RECORD_NOTE =
  "Leave the TXT record published. It is re-checked at every signup, and removing it suspends capture until the record is restored.";

/**
 * Server error codes, mapped to what the operator should do. Every code here was read off
 * the service, not guessed:
 *
 *   invalid_domain             domain.go:95
 *   public_domain              domain.go:98
 *   domain_ownership_required  domain.go:101  (403)
 *   domain_taken               domain.go:200  (409, unique index on verified domains)
 *   verification_failed        domain.go:138
 *   claim_not_found            domain.go:127  (404)
 *   edition_required           sso_handlers.go:184
 */
export function domainErrorCopy(code: string | null | undefined): string {
  switch (code) {
    case "invalid_domain":
      return "Enter a domain like acme.io — not a URL and not an email address.";
    case "public_domain":
      return "Public email domains cannot be captured. Use a domain your organization owns.";
    case "domain_ownership_required":
      return "You need a verified account at this domain to claim it. Sign in with an address at the domain, confirm the address, then claim.";
    case "domain_taken":
      return "Another organization has already verified this domain.";
    case "verification_failed":
      return "The TXT record was not found. DNS changes can take up to an hour to propagate — check the record, then try again.";
    case "claim_not_found":
      return "No claim exists for this domain in this organization. Claim it first.";
    case "edition_required":
      return "Domain capture is an enterprise capability.";
    default:
      return "Could not complete the request.";
  }
}
