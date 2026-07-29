# EPIC 11 — Production Hardening — commit-one (decision record)

Paper before product code. Decide-items D1–D5 RULED (this record); nothing builds until the paper is signed.
Re-entered from `main` `c6cf811` (EPIC 10 / S10.2 GitOps operator MERGED).

## Acceptance criterion — BETA-READINESS, not a story count

The question EPIC 11 answers: **what breaks when a stranger runs this in production, unattended, for a month?**
— upgrades, backups, restarts, resource limits, log volume, cert/key rotation, disk exhaustion, clock skew,
partial failures, and the operational surfaces that make those diagnosable. A story list is the means, not the
bar.

## Verify pass — roadmap (S11.1–4, spec'd 2026-07-15) vs shipped

All four roadmap stories are **UNBUILT**: metrics/readiness (only `/healthz` + EPIC-0 structured logging exist;
no `/metrics`, no `/readyz`), backup/restore, rate-limiting + security headers (zero rate-limiting today),
docs+upgrade (Helm NOTES only, no upgrade procedure). **The genuine delta** (the roadmap is thin + a year
stale — the 4th commit-one running to find it):
1. **The CI security-scanning tier is not in the roadmap** (it names only SECURITY.md + an external pentest).
   CodeQL/govulncheck/Trivy/SBOM/cosign are net-new — publishable, cheap, entity-independent. The delta the
   plan misses hardest.
2. **Upgrade is under-scoped as a docs sub-bullet** — a procedure + compatibility contract + tooling, the
   epic's largest item.
3. **Leader election is registered, replicas=1 by ruling** (`tunnex-cp/values.yaml:64`). S10.1 shipped
   replicas=1-deliberate; the fix lands here.
4. **Resource + failure envelopes** — the roadmap names none.
5. **Logging shipped but has a diagnosability hole** — `internal_error` doesn't log the wrapped cause +
   request_id (ledgered S11-class during the audit-nil hotfix). Observability is finishing, not greenfield.

## Standing ledger — triage (the sitting's most valuable artifact)

**HARDENING (folds into EPIC 11):** swallowed-500 logging · audit-action typed registry · WF-OP-3 drift-Event ·
CP-HA/leader-election · SECURITY.md + vuln disclosure · CI scanning tier · rate-limiting + security headers ·
helper-protocol hardening (#4/#6) · audit-helper unification (M1b root) · env-hygiene/devcontainer + restore
the e2e signal · hostNetwork + NAT-traversal deploy notes.

**FEATURE (after beta, trigger-gated — carried explicitly, not silently):** FQDN resources · device-source
rules (Feature 5) · group-membership UI · OVPN liveness telemetry · conntrack-kill on grant change · WF-C L2
zombie auto-demotion · S8.6b Windows full-tunnel re-home · R-3b-2 operator poll→watch · enable/disable audit
human-only (trigger: CR gains `disabled`).

**DEAD (superseded/shipped):** **port-scoped resources (Feature 1) — SHIPPED in S10.3** (`policyspec.PortLow/
PortHigh` + the expose/resource form). Struck on evidence, not built twice — the 4th such catch.

---

## Decide-items — RULED

### D1 — Upgrade path: FORWARD-ONLY, stated in the contract. RULED.

Downgrade is REJECTED — supporting it taxes every schema migration with a tested reverse path and every
artifact version with a backward transform, an enormous ongoing cost for a case customers resolve in practice
by **restoring a backup**. Forward-only + restore-from-backup-as-rollback is coherent and honest, and it makes
**D2 load-bearing** rather than optional. Conditions:
- **N and N-1 agent support is a CONTRACT, not an accident** — and the RED is what makes it true: an N-1
  agent's compiled artifact still compiles/loads against the N control plane (a version-window compile red,
  not a doc claim). The `ProtocolVersion` fail-static (v7) is the FLOOR; the contract is the ceiling.
- **Rolling procedure:** migrate DB → roll the CP → agents reconcile. **Never a flag-day.** (A CP outage
  never kills running tunnels — already true; the upgrade leans on it.)
- **`tunnex preflight`** checks the compatibility window BEFORE the operator commits the roll.
- Largest slice; sequenced LAST (needs D2–D4's surfaces).

### D2 — Backup/restore: the TRUST-AFTER-RESTORE invariant is the whole point. RULED.

Catastrophic case that must be **unreachable by accident**: if the master key is regenerated on restore, every
sealed column is unreadable AND the CA is lost — agents pin the CA, so the entire fleet is orphaned and every
gateway must re-enroll. Therefore:
- The backup artifact **includes the sealed master-key material + the CAs + policy + per-gateway WG
  private-key state** (node-agent state).
- The restore procedure **fails LOUD if the master key doesn't match** — the **"set-but-broken is fatal,
  never regenerate"** law (S10.1) applied at the restore seam. Never silently re-generate.
- **Wire proof (the epic's 2nd-most-important leg, after upgrade):** restore a CP from backup and prove an
  **existing agent still connects, unchanged** (the fleet is not orphaned).

### D3 — Observability floor. CONFIRMED.

- **Metrics DERIVED from the health kinds already shipped** — `apply_failing`, `desync_unknown`,
  `site_link_down`, `unsupported_ver`, `conntrack_flush_unavailable`, `hub_forwarding_not_reconciling` —
  NOT a parallel vocabulary. One truth, two renderings (the pattern this project lands on every time). Fleet
  metrics = the health kinds counted.
- **RED/USE for the CP's HTTP and DB surfaces**; `/metrics` (Prometheus) + `/readyz` on CP + node-agent.
- **Swallowed-500 fix:** the `internal_error` path logs the wrapped cause WITH the request_id
  (diagnosis-from-logs, not from a repro) — small, closes the ledgered hole.
- Log-LEVEL control; the audit-action typed registry; the operator drift-Event (WF-OP-3).

### D4 — Leader election NOW, not defer. RULED.

Replicas=1 is a documented limitation today; it becomes a PRODUCT limitation the moment a customer asks "is
the control plane HA?" and the honest answer is "no, and the fix is registered." The in-process schedulers
(failover tick, CRL rebuild, retention sweep) are the reason; leader election is the unlock, a contained
pattern. Conditions:
- **Only the scheduler loops are leader-gated — request serving stays on ALL replicas** (a follower still
  serves the API; only the ticking is single-writer).
- **Walk leg:** roll the CP under load → tunnels never drop, and **exactly one leader ticking**.

### D5 — Security posture. CONFIRMED, with the CI-blocking split ruled.

- **CI-BLOCKING:** govulncheck · CodeQL (high/critical) · gofmt/lint parity · the **SBOM (syft) + cosign
  publish** steps (a release without provenance is a release you can't attest).
- **ADVISORY:** Trivy image findings from base-image CVEs we don't control · OpenSSF Scorecard.
- **Rule: block on what we can fix; advise on what we inherit.**
- **SECURITY.md + a disclosure contact ship in the same slice.** Rate-limiting + security headers +
  helper-protocol hardening (#4/#6) land under this posture too.

---

## Slice cut (CONFIRMED as proposed)

1. **Security-CI tier + SECURITY.md + e2e-signal-restore + devcontainer.** Fastest, entity-independent. The
   e2e restoration + the devcontainer are a **DELIVERABLE, not housekeeping** — a red CI job and an unrunnable
   local web gate have degraded every gate's signal for three stories (S8.x web-gate-local-env, the S10.3 e2e
   drift, the S10.2 e2e fail). Restoring the signal is the point of the slice, not a side effect.
2. **Observability floor** — `/metrics` + `/readyz` (health-kind-derived) · swallowed-500 logging fix ·
   audit-action typed registry · WF-OP-3 drift-Event.
3. **Resource/failure envelope + leader-election** — scheduler-loops leader-gated · DB/Redis degrade-not-die
   as a public claim · Helm resource requests/limits · log/disk-growth bounds (rotation).
4. **Backup/restore + the trust-after-restore proof** — backup includes sealed material; restore fails-loud
   on master-key mismatch; wire proof an existing agent still connects.
5. **Upgrade path** — forward-only contract · N/N-1 compile red · rolling procedure · `tunnex preflight`.
   Last, largest, needs 1–4's surfaces.
6. **Docs & install/upgrade guide** — folds the hostNetwork + NAT-traversal deploy notes + the quickstart.

## Slice 2 — observability floor: verify pass + D3.1–D3.5 RULED

**Verify pass (the delta from the roadmap's one-line "S11.1 Metrics"):** the node-agent ALREADY serves
`/healthz` + `/readyz` (`apps/node/cmd/agent/main.go:325,328`) — the **CP is the laggard**, with neither
`/readyz` nor any metrics. Neither side has `/metrics`. The `internal_error` seam
(`apierr/apierr.go:42`) returns a generic envelope and logs **no wrapped cause anywhere**. Audit actions are
**18 bare string literals**, no typed registry, no drift guard.

**The count that shaped the rulings:** the advisor named 6 health kinds from memory; the enum has **13**
(`nodes/policyhealth.go`) — the 13th, `k8s_endpoints_unavailable`, was missed even by the assistant's own
first regex and caught only by re-reading completely. That is the NEVER-TRIAGE-FROM-A-TRUNCATED-READ probe
firing on both sides in one sitting, and it is the argument for D3.1: **a hand-maintained metric list drifts
the first time kind #14 lands.**

- **D3.1 — ONE gauge with a `kind` label, DERIVED from the enum, plus a drift RED. RULED.** Gauge-per-kind
  means 13 names to maintain and a 14th that silently never appears — the producer-without-consumer trap at
  the metrics tier. The enum is the SOURCE (the metric ranges over it, so omission is impossible by
  construction), and **the red is the ruling's substance: adding a health kind without a metric path must
  FAIL THE BUILD.** Census red: every value in the kind enum appears in the metric output.
- **D3.2 — separate port, unauthenticated, operator-network-only. RULED.** The Prometheus convention; keeps
  operational data off the public router entirely; composes with k8s (a Service you don't expose) and VMs
  (bind the private interface). **Conditions: the port is configurable and DEFAULTS to localhost/private,
  never `0.0.0.0`** — a metrics endpoint accidentally public on a VM gateway is an information-disclosure
  finding, and the default must make that impossible rather than merely documented against. The exposure
  model is stated in the security-posture doc alongside the gateway's.
- **D3.3 — fleet-level counts by kind only; NO org/node labels in v1. RULED.** Unbounded cardinality is how
  monitoring stacks fall over, and per-node detail already lives in the API + dashboard (one truth, two
  renderings). **Honest limit, stated: the metric answers "how many gateways are apply_failing", not "which
  ones" — the dashboard answers which.** Per-node metrics REGISTERED with trigger = a customer running their
  own Prometheus who asks for it.
- **D3.4 — log at the ONE seam, not eighteen call sites. RULED.** Where an unmapped error becomes
  `internal_error`, log the wrapped cause WITH the request_id. **Condition: verify there is exactly ONE such
  seam and CITE it — if unmapped errors can become 500s by more than one path, that is the finding, and it is
  the guard-not-mirrored class again.**
- **D3.5 — MOVED OUT OF SLICE 2 (S11-7), merged into the audit-surface unification story.** The ruling below
  was made on wrong inputs — it sized the work against **18 actions and one helper shape**; the census found
  **68 actions across 72 sites and fourteen helpers with heterogeneous signatures**. The conversion was
  attempted and REVERTED mid-flight rather than committed half-applied (a half-converted audit path on the
  surface that answers "who changed access, and when" is the worst trade available). **They are the same
  refactor discovered twice:** the vocabulary can't be typed while the helpers are fourteen, and the helpers
  can't be unified without touching every action string — so sequencing them does the same call sites twice
  with a half-typed state in between, while doing them together means one signature makes typing free.
  **Untyped constants + the census red were REFUSED** as the cheap path: they would ship the APPEARANCE of the
  ruling (no bare literals) while the property actually ruled for (a type the compiler enforces) is absent,
  and would need re-touching during the unification anyway — half a fix that must be redone is worse than a
  clean deferral. Inputs preserved in `docs/audit-unification-story.md`. Original ruling, for the record:
- **D3.5 — the audit-action registry RIDES Slice 2. RULED.** An audit trail with inconsistent action names is
  an observability defect, so it belongs here. The typed newtype + `var` block over the 18 is mechanical; the
  **drift red (every action string used in code appears in the registry) is what makes it durable** and is the
  part worth the care. Root already recorded (M1b, two audit helpers of different shapes — guard-not-mirrored).
  **If the refactor touches more than the call sites — e.g. any audit path that builds an action string
  DYNAMICALLY — surface it rather than absorbing it.**

## S11-6 — audit-helper unification RESIZED: own story, post-beta (ledger corrected)

The D3.5 census answered its own question (vocabulary CLOSED — every action originates as a source literal;
the dynamic-looking sites are branch-selected literal PAIRS, incidental, convertible to constants) and then
found something larger. **M1b was diagnosed as "two audit helpers, one taught the machine branch and one
not." There are FOURTEEN**, across nine packages: `policy` (`writeAudit`, `writeAuditAs`,
`writeSystemAudit`), `tenancy` (2 + a bespoke `deactivate`), `mfa` (2), `sites` (2), `k8s`, `ovpn`,
`invites`, `devices`.

**The number is not the finding — the EXPOSURE is.** Any future change to audit behaviour (a new actor kind,
a required field, a redaction rule, a retention constraint) must currently be mirrored **fourteen times**, and
M1b is the proof that mirroring silently fails. That is not abstract debt: it is a demonstrated failure mode
with a known instance.

**RESIZED — its own story, post-beta unless the trigger fires.** A seven-fold sizing error changes the
disposition: it touches nine packages' write paths, and though the refactor is mechanical it sits on the
surface that answers *"who changed access, and when"* for a security product — so it earns a real review, not
a slice's scoped verify. **Trigger, now SPECIFIC rather than vague: the next change to audit behaviour** —
because that change is precisely what would have to be mirrored fourteen times, so whoever picks it up is
forced into the unification anyway and is better off knowing going in.

**Sequencing benefit:** D3.5's typed registry pins the VOCABULARY first, which makes the eventual unification
strictly easier — one fewer moving part when the fourteen collapse.

## MERGE MODEL — batch, with Slice 1 as a stated EXCEPTION

EPIC 11 runs the **batch model**: build to walk-ready, one walk, then the merge train. **Slice 1 is the
deliberate exception — merged on its own** — on two grounds, recorded so the pattern is not later misread as
drift:
- **(a) It carries real security fixes.** A reachable `crypto/tls` flaw (`GO-2026-5856`) in the toolchain that
  builds *every* binary we ship, plus five more across `chi` (2), `pgx` (1) and `x/net` (2). Holding those on
  a branch means `main` stays known-vulnerable while the fix exists — the one case where batching costs more
  than it saves.
- **(b) It has no walk-shaped debt.** Slice 1's proof is CI green, not a wire leg; there is nothing for the
  epic's box-walk to discharge on its behalf. A slice whose evidence is complete at merge time does not need
  to wait for one that isn't.

Slices 2–6 rebase onto it and ride the batch as normal.

## ASSERT-PRODUCED-RESULTS — the general pattern (S11 O-1, proven on the way out)

`continue-on-error` on a JOB is almost always wrong. It suppresses *setup* failures as well as findings, so a
job that never ran is indistinguishable from one that passed clean. Two instances of the same action pin
proved it inside one slice: `trivy-action@0.28.0` (nonexistent) failed in 3s and reported **green**; after
moving `continue-on-error` to the findings **step** and adding a `test -s *.sarif` assertion, the *corrected*
pin `0.36.0` — still wrong, the tag is `v0.36.0` — failed **visibly** in 4s. **The guard caught the very next
instance of the bug it was built for.**

**The pattern:** advisory means *its findings don't block*, never *the job needn't run*. Put
`continue-on-error` on the findings-producing step only, and make every scanner assert it actually emitted
results. A scan that emits nothing must never read as a scan that found nothing.

## Box-walk teeth (beta-readiness, not "it renders")

- **D2:** restore a CP from backup → an existing agent still connects, unchanged (fleet not orphaned).
- **D4:** roll the CP under load → tunnels never drop, exactly one leader ticking.
- **D5:** a planted vuln (govulncheck/CodeQL) → the gate BLOCKS the build; a signed release verifies with cosign.
- **D1:** an N-1 agent's artifact still compiles against N (the compat-window red) + the rolling procedure on
  the wire, no flag-day.

## Status

D1–D5 RULED (this paper). Slice cut confirmed. Ledger triaged (hardening folds here; features carry
trigger-gated; Feature-1 struck dead). **Awaiting sign-off before Slice 1. Nothing builds until the paper is
signed.**
