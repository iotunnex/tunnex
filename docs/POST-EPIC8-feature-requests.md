# Post-EPIC-8 feature requests (founder, 2026-07-23) — REGISTERED, not started

Queued AFTER the WF-A/WF-B/WF-C work + the combined box-walk. Both decision-first (commit-one
paper before code). UX target: NetBird's modals (founder screenshots). DO NOT START either until
the box-walk is clean.

## Feature 1 — port-scoped resources (custom ports under TCP/UDP, not protocol-all)

Target UX: NetBird's resource modal — protocol TCP/UDP + a multi-port field (e.g. 443, 22).
Completes the 5-tuple precision the S8.7 /32-source work began.

**LEAD VERIFY (read-only, cited — run BEFORE the paper, sizes the story):** does the resource
model + compiler ALREADY carry/emit port-scoped rules? The Access UI's "CIDR : protocol : ports"
label + `policyspec.AllowEntry{PortLow, PortHigh}` (already in the wire) suggest ports exist in
the model — so is this UI-ONLY (surface the existing port grain), or model+compiler+UI? The verify
answer sizes it.

Decide-items to hold: port-LIST vs RANGE vs both · validation (1–65535) · how ports render in the
rule row.

## Feature 2 — per-rule enable/disable toggle

Target UX: NetBird's per-policy switch. Mechanism: a `disabled` boolean; the compiler SKIPS
disabled rules without deleting them.

**SEMANTIC (must be stated explicitly in the paper):** a disabled ALLOW-rule REMOVES its
permission under default-deny — it is NOT a deny-rule, NOT active blocking; disabling = "as if the
rule weren't there."

Decide-items to hold: audit on toggle (founder lean: YES — enabling/disabling access is
policy-consequential, same class as create/delete) · toggle on the ROW vs in the Edit modal.

Reds (for the eventual build): disabled → ZERO emission → default-denied · re-enable → emission
restored → flows · toggle audited.
