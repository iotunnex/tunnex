# S8.2c gateway zero-touch — WALK RECORD (demo-re-run, founder-present)

**Acceptance = the Zero-Touch Gateway Law (`docs/laws.md`):** a gateway comes online by pasting the ONE dashboard-emitted install command — nothing else. Any manual gateway networking step is a DEFECT. **Boundary clause:** the gateway VM gets zero SSH after join; the cloud console gets ONE guided visit per side (Azure UDR / AWS route-table + src/dst-check — un-codeable fabric, surfaced as guided setup, NOT a gateway touch).

**This walk re-runs the cross-cloud demo** (`walk-artifacts/cross-cloud-demo/demo-record.md`) — the same AWS↔Azure topology that took **6 manual gateway touches + 3 UI gaps** — and proves every touch is now either baked into the emitted command or is the one allowed guided cloud-console visit.

**Topology (re-use the demo's):** AWS Sydney `172.31.24.206` (VPC `172.31.0.0/16`) = hub · Azure West US `10.0.0.5` (VNet `10.0.0.0/24`) = spoke · CP public `40.65.63.141`.

**FIXTURE-FIDELITY REQUIREMENT (topology-sibling law):** the walk MUST include a **genuinely separate FORWARDED host behind a gateway** — a distinct VM/host on the site LAN, NOT the gateway host itself, NOT a dummy-in-netns. The demo's `-I` gateway-host ping hid the forward-chain asymmetry (D1); this walk must land a packet that TRAVERSES the gateway's forward chain (behind-host → remote site), so the D1 symmetric-forward + D2 src-hint are proven on real forwarded traffic. The Azure VNet's second host (`Tunnex-dev-vm` `10.0.0.4`) is the behind-host.

---

## Legs (Pawan drives; fill evidence inline)

### Leg 1 — ZERO-TOUCH JOIN (the law itself)
Enroll a gateway from the dashboard → copy the ONE emitted `docker run` → paste VERBATIM on a CLEAN cloud VM → it reaches `agent_ready` on real WireGuard with **zero edits**.
- [ ] the emitted command is a SINGLE line (no compose, no line breaks) — structurally un-mis-pasteable (the demo's double paste-failure)
- [ ] it bakes in: `--network host`, `wgctrl`, `/dev/net/tun` + NET_ADMIN, the public CP URLs, servername, token, the optional endpoint
- [ ] the CP URLs come from the CP's configured public base URL (`meta.public_base_url`) — NOT the browser origin (review #1); verify by opening the dashboard via an alias/tunnel and confirming the command still emits the real public CP address
- [ ] pasted on a clean VM → `agent_ready`, `wg show` fresh handshake, ZERO manual gateway edits
- **Evidence:** _(paste the command + the agent log + wg show)_

### Leg 2 — FORWARDED behind-host reaches the remote site (D1 + D2, the fixture-sibling proof)
From the Azure behind-host (`10.0.0.4`, NOT the gateway) ping into the AWS VPC — a plain `ping` (no `-I`), sourced naturally, TRAVERSING the Azure gateway's forward chain.
- [ ] `ping -c3 172.31.<aws-host>` from `10.0.0.4` succeeds (the FIRST forwarded cross-site packet, mesh mode)
- [ ] the Azure gateway's nft forward counter for the LAN→tunnel rule INCREMENTS (D1 symmetric forward, mesh)
- [ ] the return path sources correctly (D2 src-hint) — no overlay-address mis-source, and it SURVIVES a reconcile tick (the manual-`src`-clobber the demo hit is gone)
- **Evidence:** _(ping + nft counter + `ip route get` showing the src)_

### Leg 3 — the ONE guided cloud-console visit per side (boundary clause)
The cloud fabric (Azure UDR / AWS route-table + src/dst-check) is un-codeable → the site UI surfaces per-cloud guided setup. Prove it's ONE guided visit, not SSH-into-the-gateway.
- [ ] the dashboard shows the per-cloud "your fabric needs this route" instruction (detected/declared cloud, copy-paste snippet, doc link)
- [ ] adding the Azure UDR (one console visit) is what makes Leg 2 pass — and it's the ONLY non-gateway manual step
- [ ] NO SSH to the gateway VM after the Leg-1 join (the boundary clause holds)
- **Evidence:** _(the UI instruction screenshot + the console step)_

### Leg 4 — D3 the bridge-trap reassuring-green catch (loud, not silent)
Force the failure the demo hit silently: a gateway advertising a subnet it isn't on (or bridge-trapped wg0) → `site_subnet_unreachable` fires LOUD on the health surface even though the link handshake is fresh.
- [ ] induce it (e.g. advertise a subnet the host has no address in) → the health badge shows `site subnet unreachable` (danger), INDEPENDENT of the fresh link
- [ ] recover (host regains an address in the subnet) → the badge CLEARS without a restart
- **Evidence:** _(the badge in both states + the agent log)_

### Leg 5 — D5 site rules created from the Access builder (GAP-2 closed)
Create a `site → site` grant from the Access Add-rule modal — through the API (validation + audit), NOT the demo's raw DB insert.
- [ ] the modal offers `site` as Source AND Destination
- [ ] creating a site→site rule writes through the policy API — an audit row appears, disjointness validation rides the same path
- [ ] the enforcing gateway picks up the grant and the forward drop→accept flips on the VISIBLE chain (the D1 enforcing contrast)
- **Evidence:** _(the modal + the audit row + the enforcing ping)_

---

## Verdict
_(fill after the walk: did every one of the demo's 6 gateway touches collapse into the pasted command or the one guided console visit? Zero-Touch Law SATISFIED / not.)_

## Deferred / substitutes
- GAP-1 (in-session multi-org creation) DEFERRED — rides the org-switcher follow-on; the walk uses fresh-signup for a clean org (as the demo did), so it is NOT blocked.
- #7 (duplicate precedence ladders) DEFERRED from the fold — mechanical, no behavioral risk.
