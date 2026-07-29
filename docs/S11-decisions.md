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
