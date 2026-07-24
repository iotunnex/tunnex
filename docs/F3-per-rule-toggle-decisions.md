# Feature 3 — per-rule enable/disable toggle — commit-one (decision-first, HELD for ruling)

**Registered** in `docs/POST-EPIC8-feature-requests.md`; ruled NEXT after the post-EPIC-8 fix lane
merged (`e860268`). A small win: let an admin **disable** a policy rule without deleting it, and
re-enable it later. Nothing built — this paper states the semantic + holds the two decide-items.

## The semantic (LOCKED, founder-stated)

A `disabled` boolean on `policy_rules`. The compiler **SKIPS disabled rules** — it does not delete
them, and it does not emit a deny. Under the **default-deny** model this means:

> **A disabled ALLOW-rule REMOVES its permission — it is "as if the rule weren't there."**
> It is NOT a deny-rule, NOT active blocking. Disabling an allow simply withdraws the grant; whatever
> that rule permitted is now default-denied (unless another enabled rule still permits it).

This is the ONLY honest reading in a default-deny system: there are no deny-rules to disable, so
"disabled" can only mean "this allow no longer contributes." A re-enable restores the grant.

**Load-bearing, not out-of-hash:** disabling a rule changes the compiled artifact (its `AllowEntry`
rows vanish), so the gateway's `CanonicalHash` changes → the new policy pushes → the gateway
reconciles and the flow stops. This is a real policy change that rides the normal push/desync
machinery — NOT out-of-hash plumbing. (Contrast the S8.x out-of-hash fields; the toggle is the
opposite — it MUST move the hash.)

## The seam (cited)

- **Model:** `policy_rules` (migration `0018_zero_trust` + `0030/0033/0035/0039` extensions). Add
  `disabled boolean NOT NULL DEFAULT false`.
- **Compiler:** `apps/api/internal/enterprise/policy/compiler.go` — `Compile(s Snapshot)` iterates
  `Snapshot`'s rules (each a `Rule{ID, SrcKind, DstKind, …}`) into `AllowEntry`s. The skip is ONE
  guard: a disabled rule contributes zero `AllowEntry`s. Cleanest placement = **filter at the query/
  snapshot boundary** (`ListPolicyRules…` excludes disabled) OR **skip in `Compile`** (a `Rule.Disabled`
  field, `if r.Disabled { continue }`). Lead-decide at build: query-filter (compiler never sees a
  disabled rule — the validator-input-filtering law says be careful, but here EXCLUDING a disabled
  rule from compilation is the INTENT, not a bypass) vs compiler-skip (compiler sees all, skips —
  more testable, the `Rule.Disabled` field is explicit in the snapshot). Prefer **compiler-skip** so
  the snapshot stays complete and the skip is a visible, unit-testable predicate.
- **Service:** a `SetPolicyRuleEnabled(ruleID, enabled)` alongside `CreatePolicyRule`/`DeletePolicyRule`,
  RBAC `policy:manage` (the existing rule-management perm — never a new perm for a capability, per the
  RBAC convention). Recompile + push rides the existing rule-mutation path (create/delete already
  trigger it).
- **API:** `PATCH /organizations/{orgId}/policy-rules/{ruleId}` `{enabled: bool}` (OpenAPI-first →
  codegen). RBAC mirror regenerates.
- **Web:** `apps/web/src/pages/Access.tsx` — the toggle affordance (placement = decide-item below);
  a disabled rule renders visibly dimmed/"disabled" so the list never lies about what's enforcing.

## Decide-items — HELD for founder ruling

- **D-F3-1 — audit on toggle?** Founder prior: **YES** — enabling/disabling access is
  policy-consequential, the same class as create/delete. Audit actions `policy.rule_disabled` /
  `policy.rule_enabled` (system-actor-first convention; the actor is the admin). Held to confirm the
  prior + the two action names (or one `policy.rule_toggled` with a `{enabled}` metadata — recommend
  TWO distinct actions, mirroring create/delete, so an audit filter reads cleanly).
- **D-F3-2 — toggle placement:** the rule **row** (inline switch, one-click, most discoverable) vs the
  **Edit modal** (deliberate, groups with other rule edits). Lead: **row** for a one-click reversible
  op, but held — a modal keeps the row read-only and avoids a fat-finger disable of a live grant.

## Reds (for the eventual build)

- **compiler:** a disabled rule produces ZERO `AllowEntry`s → the gateway artifact drops those grants →
  default-denied (the exact allow, no longer present). A red over `Compile`: same snapshot with one
  rule disabled = the compiled output equals the snapshot-minus-that-rule, byte-for-byte.
- **re-enable:** flipping `disabled` back → the `AllowEntry`s reappear → flows restored (the compiled
  output equals the all-enabled snapshot).
- **hash moves:** disabling changes `CanonicalHash` for the affected node (it's a real policy change,
  not out-of-hash) — a red that the pushed hash differs enabled vs disabled.
- **audit (if D-F3-1 = yes):** the toggle emits its audit row; a disabled-then-enabled cycle leaves two
  distinct audit events.
- **RBAC:** a member without `policy:manage` gets 403 on the PATCH (no new perm invented).

## Sequence

This paper (HELD) → founder rules D-F3-1 (audit) + D-F3-2 (placement) → build (migration + compiler
skip + service + PATCH endpoint + web toggle) → gates (both editions; the compiler skip is
edition-relevant — the open build has no policy engine, so the toggle is enterprise-surfaced but the
column + API are edition-independent scaffolding) → targeted review only if it grows past the one-guard
compiler change. Then EPIC 9 (OpenVPN) commit-one, fresh sitting.
