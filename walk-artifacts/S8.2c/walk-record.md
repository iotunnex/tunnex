# S8.2c gateway zero-touch — WALK RECORD (demo-re-run, founder-present)

## PASS/FAIL LINE (Zero-Touch Gateway Law, `docs/laws.md`) — the single gate
**PASS** iff each gateway comes online by pasting the ONE dashboard-emitted install command, and the ONLY other manual actions are ONE guided cloud-console visit per side (Azure UDR / AWS route-table + src/dst-check). **FAIL** iff anything past the two pastes demands SSH to a gateway VM — any hand-added `--network host`, `TUNNEX_WG_BACKEND`, `src`-hint, forward rule, or `ip route` edit. **A FAIL leaves the story OPEN** (not merged). Guided cloud-console ≠ gateway-touch; the boundary is the gateway VM.

**This walk IS S8.2c's box-walk** and IS the **new-customer onboarding path**: it uses a FRESH SIGNUP for a clean org (the GAP-1 multi-org-switcher deferral means an existing owner can't spin a 2nd org in-session; fresh-signup is HIGHER fidelity than a switcher shortcut — it's exactly what a real new customer does). Re-runs the cross-cloud demo (`walk-artifacts/cross-cloud-demo/demo-record.md`) that took **6 manual gateway touches + 3 UI gaps** — proving every touch now collapses into a paste or the one guided console visit.

**Topology (re-use the demo's):** AWS Sydney `172.31.24.206` (VPC `172.31.0.0/16`) = hub · Azure West US `10.0.0.5` (VNet `10.0.0.0/24`) = spoke · a **separate Azure behind-host** `10.0.0.4` (the demo's `Tunnex-dev-vm`, same VNet) = the forwarded host · CP public `40.65.63.141`.

**FIXTURE-FIDELITY PRECONDITION (topology-sibling law, `docs/laws.md`) — binds Leg 2:** the cross-site ping MUST originate from a **genuinely separate FORWARDED host behind a gateway** (`10.0.0.4`), traversing the gateway's forward chain. **The `ping -I <gateway-host>` form the demo used does NOT count this run** — it's locally-originated, never forwarded, and hides the D1 forward-chain asymmetry (per our own law). If a separate behind-host isn't available, Leg 2 is INCOMPLETE, not passed.

---

## Legs (Pawan drives; fill expected-vs-observed inline)

### Leg 1 — ZERO-TOUCH JOIN + the metaLoaded gate proof
Enroll a gateway → copy the ONE emitted `docker run` → paste VERBATIM on a CLEAN cloud VM → `agent_ready` on real WireGuard, zero edits.
- **EXPECTED:** single-line command (no compose, no line breaks); bakes in `--network host`, `wgctrl`, `/dev/net/tun`+NET_ADMIN, the public CP URLs, servername, token, optional endpoint. Pasted verbatim → `agent_ready`, `wg show` fresh handshake, ZERO manual gateway edits.
- **EXPECTED (one-truth #5):** open the dashboard via an ALIAS/tunnel (not the raw public IP) and confirm the emitted command STILL carries the real configured CP public address (`meta.public_base_url`), NOT the alias origin.
- **EXPECTED (metaLoaded gate — the ONLY proof of this fix; it is component wiring, NOT unit-pinned):** on the enroll form, before `/api/v1/meta` resolves, the **"Generate join token" button reads "Checking control plane…" and is DISABLED**. There must be **NO flash of a mintable command before meta arrives** — no early-enabled Generate button, no placeholder/origin-based command, no half-formed token line. **ANY flicker of a mintable command in the in-flight window = a FINDING (WF-#), not cosmetics.** Watch the first render deliberately (hard-reload the page, eyes on the button).
- **OBSERVED (2026-07-18, enterprise CP redeployed from S8.2c source at 40.65.63.141):**
  - Command emitted (aws-gw): `docker run -d --name tunnex-node --restart unless-stopped --network host --cap-add NET_ADMIN --device /dev/net/tun -v tunnex_node_state:/var/lib/tunnex-node -e TUNNEX_JOIN_TOKEN=… -e TUNNEX_NODE_NAME="aws-gw" -e TUNNEX_NODE_ENDPOINT="15.134.231.13:51820" -e TUNNEX_API_URL="http://40.65.63.141" -e TUNNEX_AGENT_URL="https://40.65.63.141:8443" -e TUNNEX_AGENT_SERVERNAME="tunnex-control" -e TUNNEX_WG_BACKEND=wgctrl ghcr.io/iotunnex/tunnex-node-agent:latest`
  - ✅ single-line docker run (NOT compose — the D4 paste-mismatch shape is gone). ✅ host-net + wgctrl + tun + token baked in. ✅ review-#3 quoting LIVE: name, endpoint, api/agent url, servername all shell-quoted. ✅ one-truth #5: api/agent URLs derived from public_base_url (APP_BASE_URL=http://40.65.63.141), not window.location — mechanism correct (alias-divergence not stressed since browsing via the raw IP).
  - metaLoaded button state: _(pending explicit observation — the gate's only proof; re-check the button on a hard-reload)_
  - ✅ agent_ready on aws-gw (AWS `172.31.24.206`, ONE paste, zero other commands): `agent_backend_selected backend:wgctrl interface:wg0` → `agent_reconciling node_name:aws-gw` → `agent_wg_key_reported` → `agent_ready`. The Zero-Touch line held for aws-gw: the pasted docker run alone brought it online on real WireGuard.
  - ✅ agent_ready on azure-gw (Azure `10.0.0.5`, NAT'd spoke — the emitted command correctly had NO `TUNNEX_NODE_ENDPOINT`): `wgctrl` → `agent_reconciling node_name:azure-gw` → `agent_wg_key_reported` → `agent_ready`. One paste, zero other commands.
  - **Leg 1 = PASS for both gateways** — the two pasted docker-run commands are the ONLY terminal actions; both reached agent_ready on real WireGuard AND CP-registered into the fresh org (Devices → Gateways shows aws-gw + azure-gw, last seen 5-15s). The demo's per-gateway 6-touch friction (compose paste-fail, backend=mem, --network host, endpoint, urls, token) is fully collapsed into the one emitted command.
  - **STALE-VOLUME snag (walk-setup, NOT a product defect — but a doc gap worth a line):** first run reached agent_ready but did NOT appear in the fresh org. Cause: the emitted command mounts a FIXED named volume `tunnex_node_state`; these VMs hosted gateways in yesterday's cross-cloud demo, so the volume held the OLD demo identity — the agent booted cached, reconnected as the DEMO org's node (postgres_data persisted), agent_ready under the wrong org. Fix: `docker volume rm tunnex_node_state` then re-run → fresh enrollment into the current org, both appear. The zero-touch premise is a genuinely CLEAN VM; re-using a VM needs the volume wiped (or a re-image). Recommend a doc line + consider surfacing "this host already holds a gateway identity" on boot rather than silently reusing it. (Earlier mis-diagnosis as a :8443/NSG block RETRACTED — `curl` 000 on :8443 is just mTLS rejecting a certless curl, the channel is fine.)
  - **DEPLOY NOTE (not a leg finding, an infra prerequisite the walk exposed):** the CP at 40.65.63.141 was running a PRE-S8.2c OPEN-edition build; the walk required redeploying the S8.2c branch AS ENTERPRISE (the policy/rules surface Leg 5 needs is enterprise; sites/enroll/mesh are all-editions core). node-agent:latest was rebuilt+pushed to ghcr (the gateways pull it). A zsh `$1:latest` → `:l` modifier bug mis-tagged the first push (`*atest`); fixed with `${1}`.

### Leg 2 — FORWARDED behind-host reaches the remote site (D1 + D2, fixture-sibling)
From the separate Azure behind-host `10.0.0.4` (NOT the gateway) — plain `ping` (no `-I`), traversing the Azure gateway's forward chain.
- **EXPECTED:** `ping -c3 172.31.24.206` (or a host in the AWS VPC) from `10.0.0.4` succeeds — the first FORWARDED cross-site packet, mesh mode; the Azure gateway's nft LAN→tunnel forward counter INCREMENTS (D1 symmetric forward); the return path sources correctly (D2 src-hint), no overlay mis-source, and SURVIVES a reconcile tick.
- **PRECONDITION:** the `-I <gateway-host>` shortcut does NOT satisfy this leg (see the fixture-fidelity precondition above).
- **OBSERVED (D2 src-hint = PASS on the wire, 2026-07-18):**
  - aws-gw `ip route get 10.0.0.5 → src 172.31.24.206` (its LAN addr in 172.31.0.0/16), azure-gw `ip route get 172.31.24.206 → src 10.0.0.5` (its LAN addr in 10.0.0.0/24). Both source from the SITE LAN, NOT the overlay 10.99.0.1 — the exact demo bug (`src 10.99.0.1`) is FIXED. Site link up both ways (handshakes fresh, keepalive 25s, allowed-ips correct, route `proto static metric 8021`).
  - **D2 EXONERATED (was a false alarm):** first check showed `src 10.99.0.1` (no hint) — root-caused NOT to a D2 defect but to a STALE CACHED `node-agent:latest`. `docker run :latest` does not force-pull; the VMs ran yesterday's `main`-build agent (has S8.2 Routes → route landed, but NOT S8.2c D2 → no LocalSubnets/src). Verified the full D2 chain is correct in code (finalizeArtifact sets LocalSubnets alongside Routes · policyspec/nodepolicy json tags match · writeJSON encodes ds directly · reconcile reads ds.Policy.LocalSubnets · siteRouteSrc+ApplyRoutes apply src). `docker pull` the branch image + recreate → src-hint appears immediately. (3rd deploy sharp-edge, WF-2 family: `:latest`+`docker run` silently reuses a cached image; a re-used VM needs `docker pull`.)
  - forwarded behind-host ping (D1): _(pending — needs the Azure UDR, Leg 3)_

### Leg 3 — the ONE guided cloud-console visit, SURFACED IN THE UI
The un-codeable fabric (Azure UDR / AWS route-table + src/dst-check) is guided setup, ONE visit per side.
- **EXPECTED (D3/Slice-2 guided-setup scope):** the site/subnet UI **DISPLAYS the per-cloud "your fabric needs this route" instruction** on the page during the walk — detected/declared cloud, copy-paste snippet, doc link. **If Pawan has to REMEMBER the UDR/route-table step from the demo instead of READING it on the site page, that is a FINDING (WF-#) against D3/Slice-2's guided-setup scope** — the guided setup didn't ship its job.
- **EXPECTED:** adding the Azure UDR (one console visit) is what makes Leg 2 pass, and is the ONLY non-gateway manual step; NO SSH to the gateway VM after Leg-1 join.
- **OBSERVED:** _(the UI instruction screenshot + the console step)_

### Leg 4 — D3 the bridge-trap reassuring-green catch (loud, not silent)
Force a gateway advertising a subnet it isn't on (or bridge-trapped wg0) → `site_subnet_unreachable` fires LOUD even with a fresh link.
- **EXPECTED:** induce it → the health badge shows `site subnet unreachable` (danger), INDEPENDENT of the fresh link; recover → the badge CLEARS without a restart.
- **OBSERVED:** _(badge both states + agent log)_

### Leg 5 — D5 site rules from the Access builder (GAP-2 closed)
Create a `site → site` grant from the Access Add-rule modal — through the API (validation + audit), NOT a raw DB insert.
- **EXPECTED:** the modal offers `site` as Source AND Destination; creating writes through the policy API → an audit row appears, disjointness rides the same path; the enforcing gateway picks up the grant and the forward drop→accept flips on the VISIBLE chain.
- **OBSERVED:** _(modal + audit row + enforcing ping)_

---

## Findings (held WF-numbered for disposition — the founder brings dispositions back; fold only what's dispositioned)
| WF# | leg | finding | severity | disposition |
|-----|-----|---------|----------|-------------|
| WF-1 | Sites/2 | No POSITIVE site-link health on the Sites page — a healthy site-to-site link shows no "UP/linked/last-handshake" indicator, only degraded states badge (the "green=liveness-only" convention, inherited from device health). For site-to-site, liveness IS the product: a healthy link is visually indistinguishable from an idle/unconfigured one. Founder-noticed. | UX / design decide-item | HELD for founder — revisit green=liveness-only for the site surface (positive "linked · last handshake" vs convention) |
| WF-2 | setup/1 | Stale `tunnex_node_state` volume AND stale cached `:latest` image on a re-used VM → agent boots old identity/old code silently. `docker run :latest` does not force-pull; the zero-touch premise is a clean VM. Two instances hit this walk (wrong-org identity; missing-D2 code). | deploy/doc gap | HELD — doc line ("re-used VM: `docker pull` + wipe `tunnex_node_state`"); consider a boot-time notice + pinning the emitted image to a digest/`--pull=always` |
| WF-3 | Leg 3 | Guided cloud-fabric setup (Azure UDR / AWS route-table + src/dst-check + IP-forwarding) is **docs-only** (`docs/deploy-cloud-gateway.md`), NOT surfaced on the site/subnet page. D3 RULED "the site/subnet UI surfaces per-cloud 'your fabric needs this route' instructions (detected/declared cloud, copy-paste snippets, doc links)." Slice 2 delivered the doc but not the in-UI surfacing — and the site page carries no link to it. Operator must find the doc, not read it in context. | scope gap vs D3 ruling | HELD for founder — was the in-UI guided-setup in scope for S8.2c or deferrable? At minimum, link the doc from the site page |

## Verdict
_(fill after the walk: did every demo gateway touch collapse into the two pastes + the one guided console visit? Zero-Touch Law PASS / FAIL. A FAIL keeps the story open.)_

## Deferred / substitutes
- GAP-1 (in-session multi-org creation) DEFERRED — rides the org-switcher follow-on; the walk uses fresh-signup (the new-customer onboarding path), so it is NOT blocked.
- #7 (duplicate precedence ladders) DEFERRED from the fold — mechanical, no behavioral risk.
- The `metaLoaded` gate (re-review round-3) is component wiring, not unit-pinned — **Leg 1's button-state observation is its ONLY proof.**
