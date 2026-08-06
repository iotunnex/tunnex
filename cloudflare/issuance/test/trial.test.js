import { test } from "node:test";
import assert from "node:assert/strict";
import { domainOf, isPublicDomain, trialEligibility } from "../src/trial.js";

test("the domain is taken from the last @, lowercased", () => {
  assert.equal(domainOf("Ana@Acme.COM"), "acme.com");
  assert.equal(domainOf("weird@name@acme.com"), "acme.com");
});

test("sub-addressing does not change the domain", () => {
  assert.equal(domainOf("ana+trial2@acme.com"), domainOf("ana@acme.com"));
});

test("unusable addresses yield no domain rather than a wrong one", () => {
  for (const bad of ["", "no-at-sign", "@acme.com", "ana@", "ana@nodot", "ana@ acme.com", null, 42]) {
    assert.equal(domainOf(bad), null, `expected null for ${String(bad)}`);
  }
});

// A trial per gmail.com would be a trial per PERSON — the table would fill up and the control would mean
// nothing for everyone downstream of the first requester.
test("consumer providers can never identify a company", () => {
  assert.equal(isPublicDomain("gmail.com"), true);
  assert.equal(isPublicDomain("rediffmail.com"), true);
  assert.equal(isPublicDomain("acme.com"), false);
});

test("a first-time company domain is eligible", () => {
  assert.deepEqual(trialEligibility("acme.com", null), { eligible: true, reason: null });
});

test("a domain that already trialled is not eligible, and the prior date is carried", () => {
  const r = trialEligibility("acme.com", 1700000000);
  assert.equal(r.eligible, false);
  assert.equal(r.reason, "already_trialled");
  assert.equal(r.priorIssuedAt, 1700000000, "the human deciding needs to know WHEN, not just that");
});

test("a public domain is not eligible even on first contact", () => {
  assert.equal(trialEligibility("gmail.com", null).eligible, false);
});

// ⛔ THE HONEST LIMIT, ASSERTED SO IT IS NOT MISTAKEN FOR A CONTROL.
//
// D2: this stops accidents, not adversaries. A subdomain or a look-alike defeats it completely, and that
// is DOCUMENTED rather than fixed — the real gate at today's volume is that a human reads every request.
// This test exists so nobody later reads the table as "one trial per company" and relies on it.
test("⚠ a subdomain or look-alike defeats it — the limit is real and documented", () => {
  assert.equal(trialEligibility("eng.acme.com", null).eligible, true, "subdomain: not caught");
  assert.equal(trialEligibility("acme.co", null).eligible, true, "look-alike: not caught");
  assert.equal(trialEligibility("acme-inc.com", null).eligible, true, "second domain: not caught");
});
