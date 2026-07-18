# Tunnex engineering laws (central registry)

Laws minted across stories, previously scattered in `docs/S*-decisions.md`. New laws land here; existing ones get lifted over time. A law is a rule the review probes for and the build must not regress.

## ZERO-TOUCH GATEWAY LAW (founder-ratified 2026-07-18) — S8.2c acceptance bar
**A gateway is brought online by pasting the ONE install command the dashboard emits — and nothing else.** Sites, subnets, enforcing mode, the site→site grant, and a genuinely-separate host *behind* the gateway reaching a far site are ALL achieved by clicking in the dashboard — never by SSH'ing the gateway to add networking. **Any manual networking step on the gateway is a DEFECT, not a runbook line:** no hand-added `--network host`, no `TUNNEX_WG_BACKEND` flag, no `src`-hint on a route, no forward rule, no `ip route` edit. The cross-cloud demo (`walk-artifacts/cross-cloud-demo/`) re-runs clean under this bar — fresh org, two cloud VMs, the only terminal action a pasted join command — and THAT re-run is S8.2c's box-walk.
**BOUNDARY CLAUSE (S8.2c D3):** the law's boundary is the **gateway VM itself** — zero SSH to the gateway after join stands. The **cloud console gets ONE named, guided visit per side** (Azure UDR, AWS route-table + src/dst-check) — un-codeable fabric routing that the site/subnet UI SURFACES as detected/declared, copy-paste snippets + doc links. Guided ≠ manual-gateway-touch; the boundary holds.

## Fixture-fidelity law — TOPOLOGY SIBLING (minted 2026-07-18, cross-cloud demo)
**A site-to-site fixture MUST include a genuinely separate, FORWARDED host behind the gateway — not an interface inside the gateway's own netns.** S8.2's walk used dummy LANs *inside* the gateway container (`10.1.0.1` on a dummy interface); that traffic was **locally-originated, never forwarded**, so the forward chain's LAN→tunnel asymmetry (finding #5) was **invisible** — it survived a full box-walk. Locally-originated ≠ forwarded. A fixture that only originates locally cannot exercise the forward path; the first genuinely-separate behind-gateway host (the CP in the cross-cloud demo) exposed the gap immediately. (Sibling of the [[fixture-fidelity law]]: a test double must not out-capability the substrate; here, a test topology must not under-exercise the path.)

## ONE-TRUTH law (lifted to registry 2026-07-18, S8.2c) — a consumer never re-derives a fact its authority already owns
**When the control plane owns a fact authoritatively, every other layer CONSUMES it — never computes a second, independent derivation.** A second derivation agrees in the easy case and quietly diverges in exactly the hard cases the feature exists to make safe. Confirmed instances (each a review probe; a new derivation of an owned fact is a finding):
1. **Backend site-hub election** (S8.3) — the web reads the backend-elected `is_site_hub` (`electSiteHub`), never re-elects in TS.
2. **`meta.protocol_version` ceiling** (S8.3) — the CW-confirm reads the server's version ceiling, no TS hardcode.
3. **D2 `LocalSubnets`** (S8.2c) — the agent uses the CP-sent subnet for its route src-hint; it does NOT guess its own site address (egress probe / interface heuristic diverges on-prem WAN≠LAN / multi-homed).
4. **`meta.public_base_url`** (S8.2c, instance #5 of the pattern) — the gateway-enroll command derives the CP's REST/agent URLs from the CP's OWN configured public base URL, NOT `window.location`: the browser URL is whatever alias/tunnel/bare-IP the admin opened, which would bake an unreachable endpoint into the pasted zero-touch command. (Numbered #5 in the running tally though listed 4th here — instance #4 was the S8.1 D2-topology backend-hub overrule, folded into row 1's lineage.)

## Prior laws (lifted from decision docs — pointers)
- **Fixture-fidelity law** (S8.2): a test double must not be more capable than the real substrate (the fake stripped `SiteLink` on read). Contrapositive (S8.3): when the kernel genuinely reports a field, PARSE and COMPARE it (keepalive), so convergence is real not fixtured.
- **Four-word reconcile model** (S8.2): {atomic fetch, fail-static, full-sweep, keep-last-value} — any deviation is a finding.
- **DesiredState-atomic law** (S8.2): a multi-section artifact assembly error fails the WHOLE fetch; the agent holds last-good.
- **Swallowed-audit law** (S8.1): an in-tx `InsertAuditLog` error must PROPAGATE (return), else a mystery commit-rollback.
- **Validator-input-filtering law** (S8.1): never filter the disjointness validator's comparison set to exempt a collision; its value is that it can't be bypassed. **UI corollary** (S8.3): no client-side re-check — one validator, never a second copy in JS.
- **Reassuring-comment law / reassuring-empty law** (S7.x/S8.3): a load failure must never render as a reassuring empty/healthy state; the loudest line on a page must never lie in the reassuring direction (`rulesSummary` failed-state).
- **Render-floor law** (S8.3): render only wire-truth (no decorative telemetry); applies to DERIVED truth too — the UI reads the backend-elected hub, never re-elects (`electSiteHub` one-election).
- **Unlock-then-opt-in** (founder): enterprise features unlock a capability; they never turn enforcement on.
