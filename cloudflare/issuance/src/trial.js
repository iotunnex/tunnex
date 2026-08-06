// Trial eligibility — one trial per company domain.
//
// ⛔ THIS STOPS ACCIDENTS, NOT ADVERSARIES, AND ITS EXISTENCE MUST NOT READ AS THE CONTROL BEING
// IMPLEMENTED (docs/S12.4-issuance-decisions.md D2).
//
// It keys on the EMAIL DOMAIN. Subdomains, look-alike domains and a second corporate domain all defeat it
// trivially. The real control at today's volume is that a HUMAN READS EVERY REQUEST — and that reason is
// true only while the volume is small.
//
// ⚠ RE-READ D2 WHEN REQUEST VOLUME OUTGROWS INDIVIDUAL REVIEW. That is the trigger, and at that point this
// control is unimplemented while the table still sits here looking like it works. The recorded upgrade is
// DNS-TXT domain-ownership proof, reusing the S2.5 domain-capture verifier.

/** Consumer providers that can never identify a company. A trial per gmail.com would be a trial per person. */
const PUBLIC_DOMAINS = new Set([
  "gmail.com", "googlemail.com", "outlook.com", "hotmail.com", "live.com", "msn.com",
  "yahoo.com", "yahoo.co.in", "icloud.com", "me.com", "proton.me", "protonmail.com",
  "aol.com", "gmx.com", "mail.com", "yandex.com", "zoho.com", "rediffmail.com",
]);

/**
 * The domain a request is attributed to.
 *
 * ⚠ SUB-ADDRESSING IS STRIPPED (`ana+trial2@acme.com` → `acme.com`) because the local part is not the key
 * — but that is not a defence, it is tidiness. The domain is the key and the domain is what is gameable.
 */
export function domainOf(email) {
  if (typeof email !== "string") return null;
  // ⛔ TRIM THE WHOLE INPUT FIRST, THEN REJECT ANY REMAINING WHITESPACE — order matters, and getting it
  // wrong was a real defect this file shipped for one test run. Trimming the DOMAIN SEGMENT turned
  // "ana@ acme.com" into "acme.com": an invalid address silently attributed to a real company, which is
  // a trial granted against someone else's domain.
  const raw = email.trim();
  const at = raw.lastIndexOf("@");
  if (at < 1 || at === raw.length - 1) return null;
  const domain = raw.slice(at + 1).toLowerCase();
  if (/\s/.test(domain)) return null;
  if (!domain.includes(".") || domain.startsWith(".") || domain.endsWith(".")) return null;
  return domain;
}

export function isPublicDomain(domain) {
  return PUBLIC_DOMAINS.has(domain);
}

/**
 * Decide whether a domain may take a trial.
 *
 * ⛔ REFUSALS ARE ADVISORY TO A HUMAN, NOT A WALL. Every one of these lands in the admin queue with the
 * reason attached rather than being rejected at the form: a legitimate second trial (a real evaluation
 * that ran out of time, a company with two domains) is a conversation, and refusing it at the door means
 * the person most likely to become a customer meets a locked screen instead of a person.
 *
 * @param priorIssuedAt epoch seconds of this domain's last trial, or null.
 */
export function trialEligibility(domain, priorIssuedAt) {
  if (!domain) return { eligible: false, reason: "no_domain" };
  if (isPublicDomain(domain)) return { eligible: false, reason: "public_domain" };
  if (priorIssuedAt) return { eligible: false, reason: "already_trialled", priorIssuedAt };
  return { eligible: true, reason: null };
}
